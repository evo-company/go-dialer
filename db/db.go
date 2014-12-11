package db

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/warik/dialer/conf"
)

const (
	INSERT_STMT = `
		INSERT INTO cdr (
			unique_id, inner_phone_number, opponent_phone_number, call_type, company_id, disposition,
			start_time, billable_seconds, country_code
		) values (
			:unique_id, :inner_phone_number, :opponent_phone_number, :call_type, :company_id,
			:disposition, :start_time, :billable_seconds, :country_code
		)
	`
	GET_STMT    = "SELECT * FROM cdr where unique_id=$1"
	DELETE_STMT = "DELETE FROM cdr where unique_id=:unique_id"
	COUNT_STMT  = "SELECT count(*) from cdr"
)

type DBWrapper struct {
	*sqlx.DB
	*sync.RWMutex
}

var db *DBWrapper
var schema = `
	CREATE TABLE IF NOT EXISTS cdr (
		unique_id text PRIMARY KEY,
		inner_phone_number text not null,
		opponent_phone_number text not null,
		call_type text not null,
		company_id text,
		disposition text not null,
		start_time text not null,
		billable_seconds text not null,
		country_code text  not null
	);
`

type CDR struct {
	UniqueID            string `db:"unique_id"`
	InnerPhoneNumber    string `db:"inner_phone_number"`
	OpponentPhoneNumber string `db:"opponent_phone_number"`
	CallType            string `db:"call_type"`
	CompanyId           string `db:"company_id"`
	Disposition         string `db:"disposition"`
	StartTime           string `db:"start_time"`
	BillableSeconds     string `db:"billable_seconds"`
	CountryCode         string `db:"country_code"`
}

func (db *DBWrapper) AddCDR(m map[string]string) error {
	cdr := CDR{
		UniqueID:            m["UniqueID"],
		InnerPhoneNumber:    m["InnerPhoneNumber"],
		OpponentPhoneNumber: m["OpponentPhoneNumber"],
		CallType:            m["CallType"],
		CompanyId:           m["CompanyId"],
		Disposition:         m["Disposition"],
		StartTime:           m["StartTime"],
		BillableSeconds:     m["BillableSeconds"],
		CountryCode:         m["CountryCode"],
	}
	db.Lock()
	defer db.Unlock()
	return namedExec(INSERT_STMT, cdr)
}

func (db *DBWrapper) GetCDR(uniqueId string) (CDR, error) {
	db.Lock()
	defer db.Unlock()
	cdr := CDR{}
	err := db.Get(&cdr, GET_STMT, uniqueId)
	return cdr, err
}

func (db *DBWrapper) Delete(uniqueId string) error {
	db.Lock()
	defer db.Unlock()
	return namedExec(DELETE_STMT, map[string]interface{}{"unique_id": uniqueId})
}

func (db *DBWrapper) GetCount() (result int) {
	db.Lock()
	db.Unlock()
	db.Get(&result, COUNT_STMT)
	return
}

func (db *DBWrapper) SelectCDRs(limit int) (*sqlx.Rows, error) {
	db.Lock()
	defer db.Unlock()
	return db.Queryx("SELECT * FROM cdr limit $1", limit)
}

func GetDB() *DBWrapper {
	return db
}

func namedExec(stmt string, arg interface{}) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	_, err = tx.NamedExec(stmt, arg)
	tx.Commit()
	return err
}

func initDB() (db *sqlx.DB) {
	path, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	db = sqlx.MustOpen("sqlite3", filepath.Join(path, conf.CDR_DB_FILE))
	db.MustExec(schema)
	return
}

func init() {
	db = &DBWrapper{initDB(), new(sync.RWMutex)}
}
