package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	"github.com/jmoiron/sqlx"
	// required for sqlx
	_ "github.com/mattn/go-sqlite3"

	"github.com/warik/go-dialer/conf"
)

const (
	INSERT_CDR_STMT = `
		INSERT INTO cdr (
			status, caller_id, unique_id, inner_phone_number, opponent_phone_number, call_type, company_id, disposition,
			start_time, billable_seconds, country_code
		) values (
			0, :caller_id, :unique_id, :inner_phone_number, :opponent_phone_number, :call_type, :company_id,
			:disposition, :start_time, :billable_seconds, :country_code
		)
	`
	INSER_PC_STMT   = "INSERT OR IGNORE INTO phone_call (unique_id) VALUES (:unique_id)"
	GET_STMT        = "SELECT * FROM cdr where unique_id=$1"
	DELETE_PC_STMT  = "DELETE FROM phone_call where id=:id"
	DELETE_CDR_STMT = "UPDATE cdr set status = 1 where id=:id"
	COUNT_CDR_STMT  = "SELECT count(*) from cdr where status = 0"
	COUNT_PC_STMT   = "SELECT count(*) from phone_call"
)

type DBWrapper struct {
	*sqlx.DB
	*sync.RWMutex
}

var (
	once   sync.Once
	db     *DBWrapper
	schema = `
	CREATE TABLE IF NOT EXISTS cdr (
		id integer PRIMARY KEY AUTOINCREMENT,
        status integer not null,
		caller_id text not null,
		unique_id text not null,
		inner_phone_number text not null,
		opponent_phone_number text not null,
		call_type text not null,
		company_id text,
		disposition text not null,
		start_time text not null,
		billable_seconds text not null,
		country_code text  not null
	);

    CREATE TABLE IF NOT EXISTS phone_call (
        id integer PRIMARY KEY AUTOINCREMENT,
        unique_id text UNIQUE
    );
	`
)

type CDR struct {
	ID                  int    `db:"id"`
	Status              int    `db:"status"`
	UniqueID            string `db:"unique_id"`
	CallerID            string `db:"caller_id"`
	InnerPhoneNumber    string `db:"inner_phone_number"`
	OpponentPhoneNumber string `db:"opponent_phone_number"`
	CallType            string `db:"call_type"`
	CompanyId           string `db:"company_id"`
	Disposition         string `db:"disposition"`
	StartTime           string `db:"start_time"`
	BillableSeconds     string `db:"billable_seconds"`
	CountryCode         string `db:"country_code"`
}

type PhoneCall struct {
	ID       int    `db:"id"`
	UniqueID string `db:"unique_id"`
}

func (db *DBWrapper) AddCDR(m map[string]string) (sql.Result, error) {
	cdr := CDR{
		UniqueID:            m["UniqueID"],
		CallerID:            m["CallerID"],
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
	return namedExec(INSERT_CDR_STMT, cdr)
}

func (db *DBWrapper) AddPhoneCall(uniqueId string) (sql.Result, error) {
	db.Lock()
	defer db.Unlock()
	return namedExec(INSER_PC_STMT, PhoneCall{UniqueID: uniqueId})
}

func (db *DBWrapper) GetCDR(uniqueId string) (CDR, error) {
	db.Lock()
	defer db.Unlock()
	cdr := CDR{}
	err := db.Get(&cdr, GET_STMT, uniqueId)
	return cdr, err
}

func (db *DBWrapper) DeleteCdr(id int) (sql.Result, error) {
	db.Lock()
	defer db.Unlock()
	return namedExec(DELETE_CDR_STMT, map[string]interface{}{"id": id})
}

func (db *DBWrapper) DeletePhoneCall(id int) (sql.Result, error) {
	db.Lock()
	defer db.Unlock()
	return namedExec(DELETE_PC_STMT, map[string]interface{}{"id": id})
}

func (db *DBWrapper) GetCdrCount() (result int) {
	db.Lock()
	defer db.Unlock()
	db.Get(&result, COUNT_CDR_STMT)
	return
}

func (db *DBWrapper) GetPhoneCallCount() (result int) {
	db.Lock()
	defer db.Unlock()
	db.Get(&result, COUNT_PC_STMT)
	return
}

func (db *DBWrapper) SelectCDRs(limit int) ([]CDR, error) {
	db.Lock()
	defer db.Unlock()
	cdrs := []CDR{}
	rows, err := db.Queryx("SELECT * FROM cdr where status = 0 order by id desc limit $1", limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		cdr := CDR{}
		if err := rows.StructScan(&cdr); err != nil {
			return nil, err
		} else {
			cdrs = append(cdrs, cdr)
		}
	}
	return cdrs, nil
}

func (db *DBWrapper) SelectPhoneCalls(limit int) ([]PhoneCall, error) {
	db.Lock()
	defer db.Unlock()
	phoneCalls := []PhoneCall{}
	rows, err := db.Queryx("SELECT * FROM phone_call order by 1 desc limit $1", limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		phoneCall := PhoneCall{}
		if err := rows.StructScan(&phoneCall); err != nil {
			return nil, err
		} else {
			phoneCalls = append(phoneCalls, phoneCall)
		}
	}
	return phoneCalls, nil
}

func namedExec(stmt string, arg interface{}) (sql.Result, error) {
	tx, err := db.Beginx()
	if err != nil {
		return nil, err
	}
	res, err := tx.NamedExec(stmt, arg)
	tx.Commit()
	return res, err
}

func GetDB() *DBWrapper {
	once.Do(func() {
		path, _ := filepath.Abs(filepath.Dir(os.Args[0]))
		connector := sqlx.MustConnect("sqlite3",
			filepath.Join(path, conf.CDR_DB_FILE))
		connector.MustExec(schema)
		db = &DBWrapper{connector, new(sync.RWMutex)}
	})
	return db
}
