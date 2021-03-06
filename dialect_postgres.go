/*
 * Copyright 2018. bigpigeon. All rights reserved.
 * Use of this source code is governed by a MIT style
 * license that can be found in the LICENSE file.
 */

package toyorm

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
)

type PostgreSqlDialect struct{}

func (dia PostgreSqlDialect) HasTable(model *Model) ExecValue {
	return QToSExec{DefaultExec{
		"SELECT COUNT(*) FROM pg_catalog.pg_tables WHERE tablename=?",
		[]interface{}{model.Name},
	}}
}

type RawResult struct {
	ID  int64
	Err error
	//rowsAffected int64
}

func (t RawResult) LastInsertId() (int64, error) {
	return t.ID, t.Err
}

func (t RawResult) RowsAffected() (int64, error) {
	return 0, ErrNotSupportRowsAffected{}
}

func (dia PostgreSqlDialect) InsertExecutor(db Executor, exec ExecValue, debugPrinter func(string, string, error)) (sql.Result, error) {
	var result RawResult
	query := exec.Query()
	var err error
	if scanErr := db.QueryRow(query, exec.Args()...).Scan(&result.ID); scanErr == sql.ErrNoRows {
		result.Err = scanErr
	} else {
		err = scanErr
	}

	debugPrinter(query, exec.JsonArgs(), err)
	return result, err
}

func (dia PostgreSqlDialect) CreateTable(model *Model, foreign map[string]ForeignKey) (execlist []ExecValue) {
	// lazy init model
	strList := []string{}
	// use to create foreign definition

	for _, sqlField := range model.GetSqlFields() {
		var s string

		if sqlField.AutoIncrement() {
			s = fmt.Sprintf("%s SERIAL", sqlField.Column())
		} else {
			s = fmt.Sprintf("%s %s", sqlField.Column(), sqlField.SqlType())
		}
		for k, v := range sqlField.Attrs() {
			if v == "" {
				s += " " + k
			} else {
				s += " " + fmt.Sprintf("%s=%s", k, v)
			}
		}
		strList = append(strList, s)
	}
	var primaryStrList []string
	for _, p := range model.GetPrimary() {
		primaryStrList = append(primaryStrList, p.Column())
	}
	strList = append(strList, fmt.Sprintf("PRIMARY KEY(%s)", strings.Join(primaryStrList, ",")))

	for name, key := range foreign {
		f := model.GetFieldWithName(name)
		strList = append(strList,
			fmt.Sprintf(`FOREIGN KEY (%s) REFERENCES "%s"(%s)`, f.Column(), key.Model.Name, key.Field.Column()),
		)
	}

	sqlStr := fmt.Sprintf(`CREATE TABLE "%s" (%s)`,
		model.Name,
		strings.Join(strList, ","),
	)
	execlist = append(execlist, QToSExec{DefaultExec{sqlStr, nil}})

	indexStrList := []string{}
	for key, fieldList := range model.GetIndexMap() {
		fieldStrList := []string{}
		for _, f := range fieldList {
			fieldStrList = append(fieldStrList, f.Column())
		}
		indexStrList = append(indexStrList, fmt.Sprintf(`CREATE INDEX %s ON "%s"(%s)`, key, model.Name, strings.Join(fieldStrList, ",")))
	}
	uniqueIndexStrList := []string{}
	for key, fieldList := range model.GetUniqueIndexMap() {
		fieldStrList := []string{}
		for _, f := range fieldList {
			fieldStrList = append(fieldStrList, f.Column())
		}
		uniqueIndexStrList = append(uniqueIndexStrList, fmt.Sprintf(`CREATE UNIQUE INDEX %s ON "%s"(%s)`, key, model.Name, strings.Join(fieldStrList, ",")))
	}
	for _, indexStr := range indexStrList {
		execlist = append(execlist, QToSExec{DefaultExec{indexStr, nil}})
	}
	for _, indexStr := range uniqueIndexStrList {
		execlist = append(execlist, QToSExec{DefaultExec{indexStr, nil}})
	}
	return
}

func (dia PostgreSqlDialect) DropTable(m *Model) ExecValue {
	return QToSExec{DefaultExec{fmt.Sprintf(`DROP TABLE "%s"`, m.Name), nil}}
}

func (dia PostgreSqlDialect) ConditionExec(search SearchList, limit, offset int, orderBy []Column, groupBy []Column) ExecValue {
	var exec ExecValue = QToSExec{}
	if len(search) > 0 {
		searchExec := dia.SearchExec(search)
		exec = exec.Append(" WHERE "+searchExec.Source(), searchExec.Args()...)

	}
	if limit != 0 {
		exec = exec.Append(fmt.Sprintf(" LIMIT %d", limit))
	}
	if offset != 0 {
		exec = exec.Append(fmt.Sprintf(" OFFSET %d", offset))
	}
	if len(orderBy) > 0 {
		var __list []string
		for _, column := range orderBy {
			__list = append(__list, column.Column())
		}
		exec = exec.Append(" ORDER BY " + strings.Join(__list, ","))
	}
	if len(groupBy) > 0 {
		var __list []string
		for _, column := range groupBy {
			__list = append(__list, column.Column())
		}
		exec = exec.Append(" GROUP BY " + strings.Join(__list, ","))
	}
	return exec
}

func (dia PostgreSqlDialect) SearchExec(s SearchList) ExecValue {
	var stack []ExecValue
	for i := 0; i < len(s); i++ {

		var exec ExecValue = QToSExec{}
		switch s[i].Type {
		case ExprAnd:
			if len(stack) < 2 {
				panic(ErrInvalidSearchTree)
			}
			last1, last2 := stack[len(stack)-1], stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			exec = exec.Append(
				fmt.Sprintf("%s AND %s", last2.Source(), last1.Source()),
				append(append([]interface{}{}, last2.Args()...), last1.Args()...)...,
			)
		case ExprOr:
			if len(stack) < 2 {
				panic(ErrInvalidSearchTree)
			}
			last1, last2 := stack[len(stack)-1], stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			exec = exec.Append(
				fmt.Sprintf("(%s OR %s)", last2.Source(), last1.Source()),
				append(append([]interface{}{}, last2.Args()...), last1.Args()...)...,
			)
		case ExprNot:
			if len(stack) < 1 {
				panic(ErrInvalidSearchTree)
			}
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			exec = exec.Append(
				fmt.Sprintf("NOT(%s)", last.Source()),
				last.Args()...,
			)
		case ExprIgnore:
			continue

		case ExprEqual:
			exec = exec.Append(
				fmt.Sprintf("%s = ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)
		case ExprNotEqual:
			exec = exec.Append(
				fmt.Sprintf("%s <> ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)
		case ExprGreater:
			exec = exec.Append(
				fmt.Sprintf("%s > ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)
		case ExprGreaterEqual:
			exec = exec.Append(
				fmt.Sprintf("%s >= ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)

		case ExprLess:
			exec = exec.Append(
				fmt.Sprintf("%s < ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)

		case ExprLessEqual:
			exec = exec.Append(
				fmt.Sprintf("%s <= ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)
		case ExprBetween:
			vv := reflect.ValueOf(s[i].Val.Value().Interface())
			exec = exec.Append(
				fmt.Sprintf("%s BETWEEN ? AND ?", s[i].Val.Column()),
				vv.Index(0).Interface(), vv.Index(1).Interface(),
			)

		case ExprNotBetween:
			vv := reflect.ValueOf(s[i].Val.Value().Interface())
			exec = exec.Append(
				fmt.Sprintf("%s NOT BETWEEN ? AND ?", s[i].Val.Column()),
				vv.Index(0).Interface(), vv.Index(1).Interface(),
			)

		case ExprIn:
			vv := reflect.ValueOf(s[i].Val.Value().Interface())
			questionMarks := strings.TrimSuffix(strings.Repeat("?,", vv.Len()), ",")
			var args []interface{}
			for i := 0; i < vv.Len(); i++ {
				args = append(args, vv.Index(i).Interface())
			}
			exec = exec.Append(
				fmt.Sprintf("%s IN (%s)", s[i].Val.Column(), questionMarks),
				args...,
			)

		case ExprNotIn:
			vv := reflect.ValueOf(s[i].Val.Value().Interface())
			questionMarks := strings.TrimSuffix(strings.Repeat("?,", vv.Len()), ",")
			var args []interface{}
			for i := 0; i < vv.Len(); i++ {
				args = append(args, vv.Index(i).Interface())
			}
			exec = exec.Append(
				fmt.Sprintf("%s NOT IN (%s)", s[i].Val.Column(), questionMarks),
				args...,
			)

		case ExprLike:
			exec = exec.Append(
				fmt.Sprintf("%s LIKE ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)

		case ExprNotLike:
			exec = exec.Append(
				fmt.Sprintf("%s NOT LIKE ?", s[i].Val.Column()),
				s[i].Val.Value().Interface(),
			)

		case ExprNull:
			exec = exec.Append(
				fmt.Sprintf("%s IS NULL", s[i].Val.Column()),
			)

		case ExprNotNull:
			exec = exec.Append(
				fmt.Sprintf("%s IS NOT NULL", s[i].Val.Column()),
			)

		}
		stack = append(stack, exec)

	}
	return stack[0]
}

func (dia PostgreSqlDialect) FindExec(model *Model, columns []Column) ExecValue {
	var _list []string
	for _, column := range columns {
		_list = append(_list, column.Column())
	}
	var exec ExecValue = QToSExec{}
	exec = exec.Append(fmt.Sprintf(`SELECT %s FROM "%s"`, strings.Join(_list, ","), model.Name))
	return exec
}

func (dia PostgreSqlDialect) UpdateExec(model *Model, columnValues []ColumnValue) ExecValue {
	var recordList []string
	var args []interface{}
	for _, r := range columnValues {
		recordList = append(recordList, r.Column()+"=?")
		args = append(args, r.Value().Interface())
	}
	var exec ExecValue = QToSExec{}

	exec = exec.Append(
		fmt.Sprintf(`UPDATE "%s" SET %s`, model.Name, strings.Join(recordList, ",")),
		args...,
	)
	return exec
}

func (dia PostgreSqlDialect) DeleteExec(model *Model) (exec ExecValue) {
	return QToSExec{DefaultExec{fmt.Sprintf(`DELETE FROM "%s"`, model.Name), nil}}
}

func (dia PostgreSqlDialect) insertExec(model *Model, columnValues []ColumnValue) ExecValue {
	fieldStr := ""
	qStr := ""
	columnList := []string{}
	qList := []string{}
	var args []interface{}
	for _, r := range columnValues {
		columnList = append(columnList, r.Column())
		qList = append(qList, "?")
		args = append(args, r.Value().Interface())
	}
	fieldStr += strings.Join(columnList, ",")
	qStr += strings.Join(qList, ",")

	var exec ExecValue = QToSExec{}
	exec = exec.Append(
		fmt.Sprintf(`INSERT INTO "%s"(%s) VALUES(%s)`, model.Name, fieldStr, qStr),
		args...,
	)
	return exec
}

func (dia PostgreSqlDialect) InsertExec(model *Model, columnValues []ColumnValue) ExecValue {
	exec := dia.insertExec(model, columnValues)
	if len(model.GetPrimary()) == 1 && model.GetOnePrimary().AutoIncrement() {
		exec = exec.Append(" RETURNING " + model.GetOnePrimary().Column())
	}
	return exec
}

// postgres have not replace use ON CONFLICT(%s) replace
func (dia PostgreSqlDialect) ReplaceExec(model *Model, columnValues []ColumnValue) ExecValue {
	exec := dia.insertExec(model, columnValues)
	primaryKeys := model.GetPrimary()
	var primaryKeyNames []string
	for _, key := range primaryKeys {
		primaryKeyNames = append(primaryKeyNames, key.Column())
	}
	var recordList []string
	for _, r := range columnValues {
		recordList = append(recordList, r.Column()+" = Excluded."+r.Column())
	}
	exec = exec.Append(fmt.Sprintf(" ON CONFLICT(%s) DO UPDATE SET %s",
		strings.Join(primaryKeyNames, ","),
		strings.Join(recordList, ","),
	))
	return exec
}

func (dia PostgreSqlDialect) AddForeignKey(model, relationModel *Model, ForeignKeyField Field) ExecValue {

	return DefaultExec{fmt.Sprintf(
		`ALTER TABLE "%s" ADD FOREIGN KEY (%s) REFERENCES "%s"(%s)`,
		model.Name, ForeignKeyField.Column(),
		relationModel.Name, relationModel.GetOnePrimary().Column(),
	), nil}
}

func (dia PostgreSqlDialect) DropForeignKey(model *Model, ForeignKeyField Field) ExecValue {
	return QToSExec{DefaultExec{fmt.Sprintf(
		`ALTER TABLE "%s" DROP FOREIGN KEY (%s)`, model.Name, ForeignKeyField.Column(),
	), nil}}

}

func (dia PostgreSqlDialect) CountExec(model *Model) ExecValue {
	return QToSExec{DefaultExec{fmt.Sprintf(`SELECT count(*) FROM "%s"`, model.Name), nil}}
}

func (dia PostgreSqlDialect) TemplateExec(tExec BasicExec, execs map[string]BasicExec) (ExecValue, error) {
	exec, err := getTemplateExec(tExec, execs)
	if err != nil {
		return nil, err
	}
	return QToSExec{DefaultExec{exec.query, exec.args}}, nil
}
