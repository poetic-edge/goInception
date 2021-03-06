// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package session_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/hanchuanchuan/goInception/config"
	"github.com/hanchuanchuan/goInception/domain"
	"github.com/hanchuanchuan/goInception/kv"
	"github.com/hanchuanchuan/goInception/session"
	"github.com/hanchuanchuan/goInception/store/mockstore"
	"github.com/hanchuanchuan/goInception/store/mockstore/mocktikv"
	"github.com/hanchuanchuan/goInception/util/testkit"
	"github.com/hanchuanchuan/goInception/util/testleak"
	. "github.com/pingcap/check"
)

var _ = Suite(&testSessionIncSuite{})
var sql string

func TestAudit(t *testing.T) {
	TestingT(t)
}

type testSessionIncSuite struct {
	cluster   *mocktikv.Cluster
	mvccStore mocktikv.MVCCStore
	store     kv.Storage
	dom       *domain.Domain
	tk        *testkit.TestKit
}

func (s *testSessionIncSuite) SetUpSuite(c *C) {

	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	}

	testleak.BeforeTest()
	s.cluster = mocktikv.NewCluster()
	mocktikv.BootstrapWithSingleStore(s.cluster)
	s.mvccStore = mocktikv.MustNewMVCCStore()
	store, err := mockstore.NewMockTikvStore(
		mockstore.WithCluster(s.cluster),
		mockstore.WithMVCCStore(s.mvccStore),
	)
	c.Assert(err, IsNil)
	s.store = store
	session.SetSchemaLease(0)
	session.SetStatsLease(0)
	s.dom, err = session.BootstrapSession(s.store)
	c.Assert(err, IsNil)

	// config.GetGlobalConfig().Inc.Lang = "zh-CN"
	// session.SetLanguage("zh-CN")
	config.GetGlobalConfig().Inc.Lang = "en-US"
	session.SetLanguage("en-US")
}

func (s *testSessionIncSuite) TearDownSuite(c *C) {
	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	} else {
		s.dom.Close()
		s.store.Close()
		testleak.AfterTest(c)()
	}
}

func (s *testSessionIncSuite) TearDownTest(c *C) {
	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	}

	if s.tk == nil {
		s.tk = testkit.NewTestKitWithInit(c, s.store)
	}

	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.EnableDropTable = true

	res := makeSQL(s.tk, "show tables")
	c.Assert(int(s.tk.Se.AffectedRows()), Equals, 2)

	row := res.Rows()[int(s.tk.Se.AffectedRows())-1]
	sql := row[5]

	exec := `/*--user=test;--password=test;--host=127.0.0.1;--execute=1;--backup=0;--port=3306;--enable-ignore-warnings;*/
inception_magic_start;
use test_inc;
%s;
inception_magic_commit;`
	for _, name := range strings.Split(sql.(string), "\n") {
		if strings.HasPrefix(name, "show tables:") {
			continue
		}
		n := strings.Replace(name, "'", "", -1)
		res := s.tk.MustQueryInc(fmt.Sprintf(exec, "drop table "+n))
		// fmt.Println(res.Rows())
		c.Assert(int(s.tk.Se.AffectedRows()), Equals, 2)
		row := res.Rows()[int(s.tk.Se.AffectedRows())-1]
		c.Assert(row[2], Equals, "0")
		c.Assert(row[3], Equals, "Execute Successfully")
		// c.Assert(err, check.IsNil, check.Commentf("sql:%s, %v, error stack %v", sql, args, errors.ErrorStack(err)))
		// fmt.Println(row[4])
		// c.Assert(row[4].(string), IsNil)
	}

}

func (s *testSessionIncSuite) getDBVersion(c *C) int {
	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	}

	if s.tk == nil {
		s.tk = testkit.NewTestKitWithInit(c, s.store)
	}

	sql := "show variables like 'version'"

	res := makeSQL(s.tk, sql)
	c.Assert(int(s.tk.Se.AffectedRows()), Equals, 2)

	row := res.Rows()[int(s.tk.Se.AffectedRows())-1]
	versionStr := row[5].(string)

	versionStr = strings.SplitN(versionStr, "|", 2)[1]
	value := strings.Replace(versionStr, "'", "", -1)
	value = strings.TrimSpace(value)

	// if strings.Contains(strings.ToLower(value), "mariadb") {
	// 	return DBTypeMariaDB
	// }

	versionStr = strings.Split(value, "-")[0]
	versionSeg := strings.Split(versionStr, ".")
	if len(versionSeg) == 3 {
		versionStr = fmt.Sprintf("%s%02s%02s", versionSeg[0], versionSeg[1], versionSeg[2])
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			fmt.Println(err)
		}
		return version
	}

	return 50700
}

func makeSQL(tk *testkit.TestKit, sql string) *testkit.Result {
	a := `/*--user=test;--password=test;--host=127.0.0.1;--check=1;--backup=0;--port=3306;--enable-ignore-warnings;*/
inception_magic_start;
use test_inc;
%s;
inception_magic_commit;`
	return tk.MustQueryInc(fmt.Sprintf(a, sql))
}

func (s *testSessionIncSuite) testErrorCode(c *C, sql string, errors ...*session.SQLError) {
	if s.tk == nil {
		s.tk = testkit.NewTestKitWithInit(c, s.store)
	}

	res := makeSQL(s.tk, sql)
	row := res.Rows()[int(s.tk.Se.AffectedRows())-1]

	errCode := 0
	if len(errors) > 0 {
		for _, e := range errors {
			level := session.GetErrorLevel(e.Code)
			if int(level) > errCode {
				errCode = int(level)
			}
		}
	}

	if errCode > 0 {
		errMsgs := []string{}
		for _, e := range errors {
			errMsgs = append(errMsgs, e.Error())
		}
		c.Assert(row[4], Equals, strings.Join(errMsgs, "\n"))
	}

	c.Assert(row[2], Equals, strconv.Itoa(errCode))
}

func (s *testSessionIncSuite) TestBegin(c *C) {
	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	}

	tk := testkit.NewTestKitWithInit(c, s.store)
	res := tk.MustQueryInc("create table t1(id int);")

	c.Assert(int(tk.Se.AffectedRows()), Equals, 1)

	for _, row := range res.Rows() {
		c.Assert(row[2], Equals, "2")
		c.Assert(row[4], Equals, "Must start as begin statement.")
	}
}

func (s *testSessionIncSuite) TestNoSourceInfo(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	res := tk.MustQueryInc("inception_magic_start;\ncreate table t1(id int);")

	c.Assert(int(tk.Se.AffectedRows()), Equals, 1)

	for _, row := range res.Rows() {
		c.Assert(row[2], Equals, "2")
		c.Assert(row[4], Equals, "Invalid source infomation.")
	}
}

func (s *testSessionIncSuite) TestWrongDBName(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	res := tk.MustQueryInc(`/*--user=test;--password=test;--host=127.0.0.1;--check=1;--backup=0;--port=3306;--enable-ignore-warnings;*/
inception_magic_start;create table t1(id int);inception_magic_commit;`)

	c.Assert(int(tk.Se.AffectedRows()), Equals, 1)

	for _, row := range res.Rows() {
		c.Assert(row[2], Equals, "2")
		c.Assert(row[4], Equals, "Incorrect database name ''.")
	}
}

func (s *testSessionIncSuite) TestEnd(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	res := tk.MustQueryInc(`/*--user=test;--password=test;--host=127.0.0.1;--check=1;--backup=0;--port=3306;--enable-ignore-warnings;*/
inception_magic_start;use test_inc;create table t1(id int);`)

	c.Assert(int(tk.Se.AffectedRows()), Equals, 3)

	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Must end with commit.")
}

func (s *testSessionIncSuite) TestCreateTable(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	sql := ""

	config.GetGlobalConfig().Inc.CheckColumnComment = false
	config.GetGlobalConfig().Inc.CheckTableComment = false

	// 表存在
	res := makeSQL(tk, "create table t1(id int);create table t1(id int);")
	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Table 't1' already exists.")

	// 重复列
	sql = "create table test_error_code1 (c1 int, c2 int, c2 int)"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_DUP_FIELDNAME, "c2"))

	// 主键
	config.GetGlobalConfig().Inc.CheckPrimaryKey = true
	res = makeSQL(tk, "create table t1(id int);")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Set a primary key for table 't1'.")
	config.GetGlobalConfig().Inc.CheckPrimaryKey = false

	// 数据类型 警告
	sql = "create table t1(id int,c1 bit);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "c1"))

	sql = "create table t1(id int,c1 enum('red', 'blue', 'black'));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "c1"))

	sql = "create table t1(id int,c1 set('red', 'blue', 'black'));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "c1"))

	// char列建议
	config.GetGlobalConfig().Inc.MaxCharLength = 100
	sql = `create table t1(id int,c1 char(200));`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CHAR_TO_VARCHAR_LEN, "c1"))

	// 字符集
	sql = `create table t1(id int,c1 varchar(20) character set utf8,
		c2 varchar(20) COLLATE utf8_bin);`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CHARSET_ON_COLUMN, "t1", "c1"),
		session.NewErr(session.ER_CHARSET_ON_COLUMN, "t1", "c2"))

	config.GetGlobalConfig().Inc.EnableSetCharset = false
	sql = `create table t1(id int,c1 varchar(20)) character set utf8;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_CHARSET_MUST_NULL, "t1"))

	sql = `create table t1(id int,c1 varchar(20)) COLLATE utf8_bin;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_CHARSET_MUST_NULL, "t1"))

	// 关键字
	config.GetGlobalConfig().Inc.EnableIdentiferKeyword = false
	config.GetGlobalConfig().Inc.CheckIdentifier = true

	res = makeSQL(tk, "create table t1(id int, TABLES varchar(20),`c1$` varchar(20),c1234567890123456789012345678901234567890123456789012345678901234567890 varchar(20));")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Identifier 'TABLES' is keyword in MySQL.\nIdentifier 'c1$' is invalid, valid options: [a-z|A-Z|0-9|_].\nIdentifier name 'c1234567890123456789012345678901234567890123456789012345678901234567890' is too long.")

	// 列注释
	config.GetGlobalConfig().Inc.CheckColumnComment = true
	sql = "create table t1(c1 varchar(20));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_HAVE_NO_COMMENT, "c1", "t1"))

	config.GetGlobalConfig().Inc.CheckColumnComment = false

	// 表注释
	config.GetGlobalConfig().Inc.CheckTableComment = true
	res = makeSQL(tk, "create table t1(c1 varchar(20));")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Set comments for table 't1'.")

	config.GetGlobalConfig().Inc.CheckTableComment = false
	res = makeSQL(tk, "create table t1(c1 varchar(20));")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "0")

	// 无效默认值
	res = makeSQL(tk, "create table t1(id int,c1 int default '');")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Invalid default value for column 'c1'.")

	// blob/text字段
	config.GetGlobalConfig().Inc.EnableBlobType = false
	res = makeSQL(tk, "create table t1(id int,c1 blob, c2 text);")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Type blob/text is used in column 'c1'.\nType blob/text is used in column 'c2'.")

	config.GetGlobalConfig().Inc.EnableBlobType = true
	res = makeSQL(tk, "create table t1(id int,c1 blob not null);")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "TEXT/BLOB Column 'c1' in table 't1' can't  been not null.")

	// 检查默认值
	config.GetGlobalConfig().Inc.CheckColumnDefaultValue = true
	sql = "create table t1(c1 varchar(10));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_DEFAULT_ADD_COLUMN, "c1", "t1"))
	config.GetGlobalConfig().Inc.CheckColumnDefaultValue = false

	// 支持innodb引擎
	res = makeSQL(tk, "create table t1(c1 varchar(10))engine = innodb;")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "0")

	res = makeSQL(tk, "create table t1(c1 varchar(10))engine = myisam;")
	row = res.Rows()[1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Set engine to innodb for table 't1'.")

	// 时间戳 timestamp默认值
	sql = "create table t1(id int primary key,t1 timestamp default CURRENT_TIMESTAMP,t2 timestamp default CURRENT_TIMESTAMP);"
	s.testErrorCode(c, sql,
		session.NewErrf("Incorrect table definition; there can be only one TIMESTAMP column with CURRENT_TIMESTAMP in DEFAULT or ON UPDATE clause"))

	sql = "create table t1(id int primary key,t1 timestamp default CURRENT_TIMESTAMP,t2 timestamp ON UPDATE CURRENT_TIMESTAMP);"
	s.testErrorCode(c, sql)

	sql = "create table t1(id int primary key,t1 timestamp default CURRENT_TIMESTAMP,t2 date default CURRENT_TIMESTAMP);"
	s.testErrorCode(c, sql,
		session.NewErrf("Invalid default value for column '%s'.", "t2"))

	sql = "create table test_error_code1 (c1 int, aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa int)"
	s.testErrorCode(c, sql, session.NewErr(session.ER_TOO_LONG_IDENT, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	sql = "create table aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa(a int)"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TOO_LONG_IDENT, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	sql = "create table test_error_code1 (c1 int, c2 int, key aa (c1, c2), key aa (c1))"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_DUP_INDEX, "aa", "test_inc", "test_error_code1"),
		session.NewErr(session.ER_DUP_KEYNAME, "aa"))

	sql = "create table test_error_code1 (c1 int, c2 int, c3 int, key(c_not_exist))"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_NAME_FOR_INDEX, "NULL", "test_error_code1"),
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "test_error_code1.c_not_exist"))

	sql = "create table test_error_code1 (c1 int, c2 int, c3 int, primary key(c_not_exist))"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "test_error_code1.c_not_exist"))

	sql = "create table test_error_code1 (c1 int not null default '')"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DEFAULT, "c1"))

	sql = "CREATE TABLE `t` (`a` double DEFAULT 1.0 DEFAULT 2.0 DEFAULT now());"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DEFAULT, "a"))

	sql = "CREATE TABLE `t` (`a` double DEFAULT now());"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DEFAULT, "a"))

	// 字符集
	config.GetGlobalConfig().Inc.EnableSetCharset = false
	config.GetGlobalConfig().Inc.SupportCharset = ""
	sql = "create table t1(a int) character set utf8;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_CHARSET_MUST_NULL, "t1"))

	config.GetGlobalConfig().Inc.EnableSetCharset = true
	config.GetGlobalConfig().Inc.SupportCharset = "utf8mb4"
	sql = "create table t1(a int) character set utf8;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_NAMES_MUST_UTF8, "utf8mb4"))

	config.GetGlobalConfig().Inc.EnableSetCharset = true
	config.GetGlobalConfig().Inc.SupportCharset = "utf8,utf8mb4"
	sql = "create table t1(a int) character set utf8;"
	s.testErrorCode(c, sql)

	config.GetGlobalConfig().Inc.EnableSetCharset = true
	config.GetGlobalConfig().Inc.SupportCharset = "utf8,utf8mb4"
	sql = "create table t1(a int) character set laitn1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_NAMES_MUST_UTF8, "utf8,utf8mb4"))

	// 外键
	sql = "create table test_error_code (a int not null ,b int not null,c int not null, d int not null, foreign key (b, c) references product(id));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_NAME_FOR_INDEX, "NULL", "test_error_code"),
		session.NewErr(session.ER_FOREIGN_KEY, "test_error_code"))

	sql = "create table test_error_code (a int not null ,b int not null,c int not null, d int not null, foreign key fk_1(b, c) references product(id));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_FOREIGN_KEY, "test_error_code"))

	sql = "create table test_error_code_2;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_MUST_HAVE_COLUMNS))

	sql = "create table test_error_code_2 (unique(c1));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_MUST_HAVE_COLUMNS))

	sql = "create table test_error_code_2(c1 int, c2 int, c3 int, primary key(c1), primary key(c2));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_MULTIPLE_PRI_KEY))

	fmt.Println("数据库版本: ", s.getDBVersion(c))

	indexMaxLength := 767
	if s.getDBVersion(c) >= 50700 {
		indexMaxLength = 3072
	}

	config.GetGlobalConfig().Inc.EnableBlobType = false
	sql = "create table test_error_code_3(pt text ,primary key (pt));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_USE_TEXT_OR_BLOB, "pt"),
		session.NewErr(session.ER_TOO_LONG_KEY, "", indexMaxLength))

	config.GetGlobalConfig().Inc.EnableBlobType = true
	// 索引长度
	sql = "create table test_error_code_3(a text, unique (a(3073)));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_NAME_FOR_INDEX, "NULL", "test_error_code_3"),
		session.NewErr(session.ER_TOO_LONG_KEY, "", indexMaxLength))

	sql = "create table test_error_code_3(c1 int,c2 text, unique uq_1(c1,c2(3069)));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TOO_LONG_KEY, "uq_1", indexMaxLength))

	// sql = "create table test_error_code_3(c1 int,c2 text, unique uq_1(c1,c2(3068)));"
	// if indexMaxLength == 3072 {
	// 	s.testErrorCode(c, sql)
	// } else {
	// 	s.testErrorCode(c, sql,
	// 		session.NewErr(session.ER_TOO_LONG_KEY, "", indexMaxLength))
	// }

	sql = "create table test_error_code_3(pt blob ,primary key (pt));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_BLOB_USED_AS_KEY, "pt"))

	sql = "create table test_error_code_3(`id` int, key `primary`(`id`));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_NAME_FOR_INDEX, "primary", "test_error_code_3"))

	sql = "create table t2(c1.c2 varchar(10));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_TABLE_NAME, "c1"))

	sql = "create table t2 (c1 int default null primary key , age int);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_PRIMARY_CANT_HAVE_NULL))

	sql = "create table t2 (id int null primary key , age int);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_PRIMARY_CANT_HAVE_NULL))

	sql = "create table t2 (id int default null, age int, primary key(id));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_PRIMARY_CANT_HAVE_NULL))

	sql = "create table t2 (id int null, age int, primary key(id));"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_PRIMARY_CANT_HAVE_NULL))

	sql = `drop table if exists t1;create table t1(
id int auto_increment comment 'test',
crtTime datetime not null DEFAULT CURRENT_TIMESTAMP comment 'test',
uptTime datetime DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP comment 'test',
primary key(id)) comment 'test';`
	s.testErrorCode(c, sql)
}

func (s *testSessionIncSuite) TestDropTable(c *C) {
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.EnableDropTable = false
	sql := ""
	sql = "create table t1(id int);drop table t1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CANT_DROP_TABLE, "t1"))

	config.GetGlobalConfig().Inc.EnableDropTable = true

	sql = "create table t1(id int);drop table t1;"
	s.testErrorCode(c, sql)
}

func (s *testSessionIncSuite) TestAlterTableAddColumn(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckColumnComment = false
	config.GetGlobalConfig().Inc.CheckTableComment = false
	config.GetGlobalConfig().Inc.EnableDropTable = true

	res := makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c1 int;")
	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")

	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c1 int;alter table t1 add column c1 int;")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Column 't1.c1' have existed.")

	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c1 int first;alter table t1 add column c2 int after c1;")
	for _, row := range res.Rows() {
		c.Assert(row[2], Not(Equals), "2")
	}

	// after 不存在的列
	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c2 int after c1;")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Column 't1.c1' not existed.")

	// 数据类型 警告
	sql = "drop table if exists t1;create table t1(id int);alter table t1 add column c2 bit;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "c2"))

	sql = "drop table if exists t1;create table t1(id int);alter table t1 add column c2 enum('red', 'blue', 'black');"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "c2"))

	sql = "drop table if exists t1;create table t1(id int);alter table t1 add column c2 set('red', 'blue', 'black');"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "c2"))

	// char列建议
	config.GetGlobalConfig().Inc.MaxCharLength = 100
	sql = `drop table if exists t1;create table t1(id int);
		alter table t1 add column c1 char(200);`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CHAR_TO_VARCHAR_LEN, "c1"))

	// 字符集
	res = makeSQL(tk, `drop table if exists t1;create table t1(id int);
		alter table t1 add column c1 varchar(20) character set utf8;
		alter table t1 add column c2 varchar(20) COLLATE utf8_bin;`)
	row = res.Rows()[int(tk.Se.AffectedRows())-2]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Not Allowed set charset for column 't1.c1'.")

	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Not Allowed set charset for column 't1.c2'.")

	// 关键字
	config.GetGlobalConfig().Inc.EnableIdentiferKeyword = false
	config.GetGlobalConfig().Inc.CheckIdentifier = true

	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column TABLES varchar(20);alter table t1 add column `c1$` varchar(20);alter table t1 add column c1234567890123456789012345678901234567890123456789012345678901234567890 varchar(20);")
	row = res.Rows()[int(tk.Se.AffectedRows())-3]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Identifier 'TABLES' is keyword in MySQL.")
	row = res.Rows()[int(tk.Se.AffectedRows())-2]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Identifier 'c1$' is invalid, valid options: [a-z|A-Z|0-9|_].")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Identifier name 'c1234567890123456789012345678901234567890123456789012345678901234567890' is too long.")

	// 列注释
	config.GetGlobalConfig().Inc.CheckColumnComment = true
	sql = "drop table if exists t1;create table t1(id int);alter table t1 add column c1 varchar(20);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_HAVE_NO_COMMENT, "c1", "t1"))
	config.GetGlobalConfig().Inc.CheckColumnComment = false

	// 无效默认值
	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c1 int default '';")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Invalid default value for column 'c1'.")

	// blob/text字段
	config.GetGlobalConfig().Inc.EnableBlobType = false
	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c1 blob;alter table t1 add column c2 text;")
	row = res.Rows()[int(tk.Se.AffectedRows())-2]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Type blob/text is used in column 'c1'.")

	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Type blob/text is used in column 'c2'.")

	config.GetGlobalConfig().Inc.EnableBlobType = true
	res = makeSQL(tk, "drop table if exists t1;create table t1(id int);alter table t1 add column c1 blob not null;")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "TEXT/BLOB Column 'c1' in table 't1' can't  been not null.")

	// 检查默认值
	config.GetGlobalConfig().Inc.CheckColumnDefaultValue = true
	sql = "drop table if exists t1;create table t1(id int);alter table t1 add column c1 varchar(10);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_DEFAULT_ADD_COLUMN, "c1", "t1"))
	config.GetGlobalConfig().Inc.CheckColumnDefaultValue = false

	sql = "drop table if exists t1;create table t1(id int primary key , age int);"
	s.testErrorCode(c, sql)

	// // add column
	sql = "drop table if exists t1;create table t1 (c1 int primary key);alter table t1 add column c1 int"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_EXISTED, "t1.c1"))

	sql = "drop table if exists t1;create table t1 (c1 int primary key);alter table t1 add column aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa int"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TOO_LONG_IDENT, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	sql = "drop table if exists t1;alter table t1 comment 'test comment'"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_NOT_EXISTED_ERROR, "test_inc.t1"))

	sql = "drop table if exists t1;create table t1 (c1 int primary key);alter table t1 add column `a ` int ;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_IDENT, "a "),
		session.NewErr(session.ER_WRONG_COLUMN_NAME, "a "))

	sql = "drop table if exists t1;create table t1 (c1 int primary key);alter table t1 add column c2 int on update current_timestamp;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_ON_UPDATE, "c2"))

	sql = "drop table if exists t1;create table t1(c2 int on update current_timestamp);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_ON_UPDATE, "c2"))

	sql = "drop table if exists t1;create table t1 (c1 int primary key);alter table t1 add c2 json;"
	s.testErrorCode(c, sql)

}

func (s *testSessionIncSuite) TestAlterTableAlterColumn(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	res := makeSQL(tk, "create table t1(id int);alter table t1 alter column id set default '';")
	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Invalid default value for column 'id'.")

	res = makeSQL(tk, "create table t1(id int);alter table t1 alter column id set default '1';")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")

	res = makeSQL(tk, "create table t1(id int);alter table t1 alter column id drop default ;alter table t1 alter column id set default '1';")
	row = res.Rows()[int(tk.Se.AffectedRows())-2]
	c.Assert(row[2], Equals, "0")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")
}

func (s *testSessionIncSuite) TestAlterTableModifyColumn(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckColumnComment = false
	config.GetGlobalConfig().Inc.CheckTableComment = false

	res := makeSQL(tk, "create table t1(id int,c1 int);alter table t1 modify column c1 int first;")
	c.Assert(int(tk.Se.AffectedRows()), Equals, 3)
	for _, row := range res.Rows() {
		c.Assert(row[2], Not(Equals), "2")
	}

	res = makeSQL(tk, "create table t1(id int,c1 int);alter table t1 modify column id int after c1;")
	c.Assert(int(tk.Se.AffectedRows()), Equals, 3)
	for _, row := range res.Rows() {
		c.Assert(row[2], Not(Equals), "2")
	}

	// after 不存在的列
	sql = "create table t1(id int);alter table t1 modify column c1 int after id;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t1.c1"))

	sql = "create table t1(id int,c1 int);alter table t1 modify column c1 int after id1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t1.id1"))

	// 数据类型 警告
	sql = "create table t1(id bit);alter table t1 modify column id bit;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "id"))

	sql = "create table t1(id enum('red', 'blue'));alter table t1 modify column id enum('red', 'blue', 'black');"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "id"))

	sql = "create table t1(id set('red'));alter table t1 modify column id set('red', 'blue', 'black');"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_INVALID_DATA_TYPE, "id"))

	// char列建议
	config.GetGlobalConfig().Inc.MaxCharLength = 100
	sql = `create table t1(id int,c1 char(10));
		alter table t1 modify column c1 char(200);`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CHAR_TO_VARCHAR_LEN, "c1"))

	// 字符集
	res = makeSQL(tk, `create table t1(id int,c1 varchar(20));
		alter table t1 modify column c1 varchar(20) character set utf8;
		alter table t1 modify column c1 varchar(20) COLLATE utf8_bin;`)
	row := res.Rows()[int(tk.Se.AffectedRows())-2]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Not Allowed set charset for column 't1.c1'.")

	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Not Allowed set charset for column 't1.c1'.")

	// 列注释
	config.GetGlobalConfig().Inc.CheckColumnComment = true
	res = makeSQL(tk, "create table t1(id int,c1 varchar(10));alter table t1 modify column c1 varchar(20);")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "Column 'c1' in table 't1' have no comments.")

	config.GetGlobalConfig().Inc.CheckColumnComment = false

	// 无效默认值
	res = makeSQL(tk, "create table t1(id int,c1 int);alter table t1 modify column c1 int default '';")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Invalid default value for column 'c1'.")

	// blob/text字段
	config.GetGlobalConfig().Inc.EnableBlobType = false
	res = makeSQL(tk, "create table t1(id int,c1 varchar(10));alter table t1 modify column c1 blob;alter table t1 modify column c1 text;")
	row = res.Rows()[int(tk.Se.AffectedRows())-2]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Type blob/text is used in column 'c1'.")

	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "2")
	c.Assert(row[4], Equals, "Type blob/text is used in column 'c1'.")

	config.GetGlobalConfig().Inc.EnableBlobType = true
	res = makeSQL(tk, "create table t1(id int,c1 blob);alter table t1 modify column c1 blob not null;")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "1")
	c.Assert(row[4], Equals, "TEXT/BLOB Column 'c1' in table 't1' can't  been not null.")

	// 检查默认值
	config.GetGlobalConfig().Inc.CheckColumnDefaultValue = true
	sql = "create table t1(id int,c1 varchar(5));alter table t1 modify column c1 varchar(10);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_DEFAULT_ADD_COLUMN, "c1", "t1"))
	config.GetGlobalConfig().Inc.CheckColumnDefaultValue = false

	// 变更类型
	sql = "create table t1(c1 int,c1 int);alter table t1 modify column c1 varchar(10);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CHANGE_COLUMN_TYPE, "t1.c1", "int(11)", "varchar(10)"))

	sql = "create table t1(c1 char(100));alter table t1 modify column c1 char(20);"
	s.testErrorCode(c, sql)

	sql = "create table t1(c1 varchar(100));alter table t1 modify column c1 varchar(10);"
	s.testErrorCode(c, sql)

	sql = "create table t1(id int primary key,t1 timestamp default CURRENT_TIMESTAMP,t2 timestamp ON UPDATE CURRENT_TIMESTAMP);"
	s.testErrorCode(c, sql)

	// modify column
	sql = "create table t1(id int primary key,c1 int);alter table t1 modify testx.t1.c1 int"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_DB_NAME, "testx"))

	sql = "create table t1(id int primary key,c1 int);alter table t1 modify t.c1 int"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_TABLE_NAME, "t"))
}

func (s *testSessionIncSuite) TestAlterTableDropColumn(c *C) {
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()
	sql := ""
	sql = "create table t1(id int,c1 int);alter table t1 drop column c2;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t1.c2"))

	sql = "create table t1(id int,c1 int);alter table t1 drop column c1;"
	s.testErrorCode(c, sql)

	// // drop column
	sql = "create table t2 (id int null);alter table t2 drop c1"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t2.c1"))

	sql = "create table t2 (id int null);alter table t2 drop id;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ErrCantRemoveAllFields))
}

func (s *testSessionIncSuite) TestInsert(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckInsertField = false

	// 表不存在
	sql = "insert into t1 values(1,1);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_NOT_EXISTED_ERROR, "test_inc.t1"))

	// 列数不匹配
	sql = "create table t1(id int,c1 int);insert into t1(id) values(1,1);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_VALUE_COUNT_ON_ROW, 1))

	sql = "create table t1(id int,c1 int);insert into t1(id) values(1),(2,1);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_VALUE_COUNT_ON_ROW, 2))

	sql = "create table t1(id int,c1 int not null);insert into t1(id,c1) select 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WRONG_VALUE_COUNT_ON_ROW, 1))

	// 列重复
	sql = "create table t1(id int,c1 int);insert into t1(id,id) values(1,1);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_FIELD_SPECIFIED_TWICE, "id", "t1"))

	sql = "create table t1(id int,c1 int);insert into t1(id,id) select 1,1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_FIELD_SPECIFIED_TWICE, "id", "t1"))

	// 字段警告
	config.GetGlobalConfig().Inc.CheckInsertField = true
	sql = "create table t1(id int,c1 int);insert into t1 values(1,1);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_INSERT_FIELD))
	config.GetGlobalConfig().Inc.CheckInsertField = false

	sql = "create table t1(id int,c1 int);insert into t1(id) values();"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_INSERT_VALUES))

	// 列不允许为空
	sql = "create table t1(id int,c1 int not null);insert into t1(id,c1) values(1,null);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_BAD_NULL_ERROR, "test_inc.t1.c1", 1))

	sql = "create table t1(id int,c1 int not null default 1);insert into t1(id,c1) values(1,null);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_BAD_NULL_ERROR, "test_inc.t1.c1", 1))

	// insert select 表不存在
	sql = "create table t1(id int,c1 int );insert into t1(id,c1) select 1,null from t2;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_NOT_EXISTED_ERROR, "test_inc.t2"))
	// select where
	config.GetGlobalConfig().Inc.CheckDMLWhere = true
	sql = "create table t1(id int,c1 int );insert into t1(id,c1) select 1,null from t1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_NO_WHERE_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLWhere = false

	// limit
	config.GetGlobalConfig().Inc.CheckDMLLimit = true
	sql = "create table t1(id int,c1 int );insert into t1(id,c1) select 1,null from t1 limit 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_LIMIT_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLLimit = false

	// order by rand()
	// config.GetGlobalConfig().Inc.CheckDMLOrderBy = true
	sql = "create table t1(id int,c1 int );insert into t1(id,c1) select 1,null from t1 order by rand();"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_ORDERY_BY_RAND))

	// config.GetGlobalConfig().Inc.CheckDMLOrderBy = false

	// 受影响行数
	res := makeSQL(tk, "create table t1(id int,c1 int);insert into t1 values(1,1),(2,2);")
	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")
	c.Assert(row[6], Equals, "2")

	res = makeSQL(tk, "create table t1(id int,c1 int );insert into t1(id,c1) select 1,null;")
	row = res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")
	c.Assert(row[6], Equals, "1")

	sql = "create table t1(c1 char(100) not null);insert into t1(c1) values(null);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_BAD_NULL_ERROR, "test_inc.t1.c1", 1))

	sql = "create table t1(c1 char(100) not null);insert into t1(c1) select t1.c1 from t1 inner join t1 on t1.id=t1.id;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ErrNonUniqTable, "t1"))

	sql = "create table t1(c1 char(100) not null);insert into t1(c1) select t1.c1 from t1 limit 1 union all select t1.c1 from t1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ErrWrongUsage, "UNION", "LIMIT"))

	sql = "create table t1(c1 char(100) not null);insert into t1(c1) select t1.c1 from t1 order by 1 union all select t1.c1 from t1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ErrWrongUsage, "UNION", "ORDER BY"))
}

func (s *testSessionIncSuite) TestUpdate(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckInsertField = false

	// 表不存在
	sql = "update t1 set c1 = 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_NOT_EXISTED_ERROR, "test_inc.t1"))

	sql = "create table t1(id int);update t1 set c1 = 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "c1"))

	sql = "create table t1(id int,c1 int);update t1 set c1 = 1,c2 = 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t1.c2"))

	sql = `create table t1(id int primary key,c1 int);
		create table t2(id int primary key,c1 int,c2 int);
		update t1 inner join t2 on t1.id=t2.id2  set t1.c1=t2.c1 where c11=1;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t2.id2"),
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "c11"))

	sql = `create table t1(id int primary key,c1 int);
		create table t2(id int primary key,c1 int,c2 int);
		update t1,t2 t3 set t1.c1=t2.c3 where t1.id=t3.id;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t2.c3"))

	sql = `create table t1(id int primary key,c1 int);
		create table t2(id int primary key,c1 int,c2 int);
		update t1,t2 t3 set t1.c1=t2.c3 where t1.id=t3.id;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t2.c3"))

	// where
	config.GetGlobalConfig().Inc.CheckDMLWhere = true
	sql = "create table t1(id int,c1 int);update t1 set c1 = 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_NO_WHERE_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLWhere = false

	// limit
	config.GetGlobalConfig().Inc.CheckDMLLimit = true
	sql = "create table t1(id int,c1 int);update t1 set c1 = 1 limit 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_LIMIT_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLLimit = false

	// order by rand()
	config.GetGlobalConfig().Inc.CheckDMLOrderBy = true
	sql = "create table t1(id int,c1 int);update t1 set c1 = 1 order by rand();"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_ORDERBY_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLOrderBy = false

	// 受影响行数
	res := makeSQL(tk, "create table t1(id int,c1 int);update t1 set c1 = 1;")
	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")
	c.Assert(row[6], Equals, "0")

	// res = makeSQL(tk, "create table t1(id int primary key,c1 int);insert into t1 values(1,1),(2,2);update t1 set c1 = 1 where id = 1;")
	// row = res.Rows()[int(tk.Se.AffectedRows())-1]
	// c.Assert(row[2], Equals, "0")
	// c.Assert(row[6], Equals, "1")

	sql = `drop table if exists table1;drop table if exists table2;
		create table table1(id int primary key,c1 int);
		create table table2(id int primary key,c1 int,c2 int);
		update table1 t1,table2 t2 set t1.c1=t2.c1 where t1.id=t2.id;`
	s.testErrorCode(c, sql)

}

func (s *testSessionIncSuite) TestDelete(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckInsertField = false

	// 表不存在
	sql = "delete from t1 where c1 = 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_NOT_EXISTED_ERROR, "test_inc.t1"))

	// res = makeSQL(tk, "create table t1(id int);delete from t1 where c1 = 1;")
	// row = res.Rows()[int(tk.Se.AffectedRows())-1]
	// c.Assert(row[2], Equals, "2")
	// c.Assert(row[4], Equals, "Column 'c1' not existed.")

	// res = makeSQL(tk, "create table t1(id int,c1 int);delete from t1 where c1 = 1 and c2 = 1;")
	// row = res.Rows()[int(tk.Se.AffectedRows())-1]
	// c.Assert(row[2], Equals, "2")
	// c.Assert(row[4], Equals, "Column 't1.c2' not existed.")

	// where
	config.GetGlobalConfig().Inc.CheckDMLWhere = true
	sql = "create table t1(id int,c1 int);delete from t1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_NO_WHERE_CONDITION))

	config.GetGlobalConfig().Inc.CheckDMLWhere = false

	// limit
	config.GetGlobalConfig().Inc.CheckDMLLimit = true
	sql = "create table t1(id int,c1 int);delete from t1 where id = 1 limit 1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_LIMIT_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLLimit = false

	// order by rand()
	config.GetGlobalConfig().Inc.CheckDMLOrderBy = true
	sql = "create table t1(id int,c1 int);delete from t1 where id = 1 order by rand();"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_WITH_ORDERBY_CONDITION))
	config.GetGlobalConfig().Inc.CheckDMLOrderBy = false

	// 表不存在
	sql = `create table t1(id int primary key,c1 int);
		create table t2(id int primary key,c1 int,c2 int);
		delete from t3 where id1 =1;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_NOT_EXISTED_ERROR, "test_inc.t3"))

	sql = `create table t1(id int primary key,c1 int);
		create table t2(id int primary key,c1 int,c2 int);
		delete from t1 where id1 =1;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "id1"))

	sql = `create table t1(id int primary key,c1 int);
		create table t2(id int primary key,c1 int,c2 int);
		delete t2 from t1 inner join t2 on t1.id=t2.id2 where c11=1;`
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t2.id2"))

	// 受影响行数
	res := makeSQL(tk, "create table t1(id int,c1 int);delete from t1 where id = 1;")
	row := res.Rows()[int(tk.Se.AffectedRows())-1]
	c.Assert(row[2], Equals, "0")
	c.Assert(row[6], Equals, "0")
}

func (s *testSessionIncSuite) TestCreateDataBase(c *C) {
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.EnableDropDatabase = false
	// 不存在
	sql = "drop database if exists test1111111111111111111;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CANT_DROP_DATABASE, "test1111111111111111111"))

	sql = "drop database test1111111111111111111;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CANT_DROP_DATABASE, "test1111111111111111111"))
	config.GetGlobalConfig().Inc.EnableDropDatabase = true

	sql = "drop database if exists test1111111111111111111;create database test1111111111111111111;"
	s.testErrorCode(c, sql)

	// 存在
	sql = "create database test1111111111111111111;create database test1111111111111111111;"
	s.testErrorCode(c, sql,
		session.NewErrf("数据库'test1111111111111111111'已存在."))

	// if not exists 创建
	sql = "create database if not exists test1111111111111111111;create database if not exists test1111111111111111111;"
	s.testErrorCode(c, sql)

	// create database
	sql := "create database aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TOO_LONG_IDENT, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	sql = "create database mysql"
	s.testErrorCode(c, sql,
		session.NewErrf("数据库'%s'已存在.", "mysql"))

	// 字符集
	config.GetGlobalConfig().Inc.EnableSetCharset = false
	config.GetGlobalConfig().Inc.SupportCharset = ""
	sql = "drop database test1;create database test1 character set utf8;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CANT_SET_CHARSET, "utf8"))

	config.GetGlobalConfig().Inc.SupportCharset = "utf8mb4"
	sql = "drop database test1;create database test1 character set utf8;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CANT_SET_CHARSET, "utf8"),
		session.NewErr(session.ER_NAMES_MUST_UTF8, "utf8mb4"))

	config.GetGlobalConfig().Inc.EnableSetCharset = true
	config.GetGlobalConfig().Inc.SupportCharset = "utf8,utf8mb4"
	sql = "drop database test1;create database test1 character set utf8;"
	s.testErrorCode(c, sql)

	config.GetGlobalConfig().Inc.EnableSetCharset = true
	config.GetGlobalConfig().Inc.SupportCharset = "utf8,utf8mb4"
	sql = "drop database test1;create database test1 character set laitn1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_NAMES_MUST_UTF8, "utf8,utf8mb4"))
}

func (s *testSessionIncSuite) TestRenameTable(c *C) {

	// 不存在
	sql = "drop table if exists t1;create table t1(id int primary key);alter table t1 rename t2;"
	s.testErrorCode(c, sql)

	sql = "drop table if exists t1;create table t1(id int primary key);rename table t1 to t2;"
	s.testErrorCode(c, sql)

	// 存在
	sql = "drop table if exists t1;create table t1(id int primary key);rename table t1 to t1;"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_TABLE_EXISTS_ERROR, "t1"))
}

func (s *testSessionIncSuite) TestCreateView(c *C) {

	sql = "create table t1(id int primary key);create view v1 as select * from t1;"
	s.testErrorCode(c, sql,
		session.NewErrf("命令禁止! 无法创建视图'v1'."))
}

func (s *testSessionIncSuite) TestAlterTableAddIndex(c *C) {
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckColumnComment = false
	config.GetGlobalConfig().Inc.CheckTableComment = false

	// add index
	sql = "create table t1(id int);alter table t1 add index idx (c1)"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_COLUMN_NOT_EXISTED, "t1.c1"))

	sql = "create table t1(id int,c1 int);alter table t1 add index idx (c1);"
	s.testErrorCode(c, sql)

	sql = "create table t1(id int,c1 int);alter table t1 add index idx (c1);alter table t1 add index idx (c1);"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_DUP_INDEX, "idx", "test_inc", "t1"))

}

func (s *testSessionIncSuite) TestAlterTableDropIndex(c *C) {
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.CheckColumnComment = false
	config.GetGlobalConfig().Inc.CheckTableComment = false

	// drop index
	sql = "create table t1(id int);alter table t1 drop index idx"
	s.testErrorCode(c, sql,
		session.NewErr(session.ER_CANT_DROP_FIELD_OR_KEY, "t1.idx"))

	sql = "create table t1(c1 int);alter table t1 add index idx (c1);alter table t1 drop index idx;"
	s.testErrorCode(c, sql)
}
