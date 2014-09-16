package db

import (
	"log"

	"github.com/boltdb/bolt"
	"github.com/warik/dialer/conf"
	"github.com/warik/gami"
)

var db *bolt.DB
var PutChan chan gami.Message
var DeleteChan chan gami.Message

func GetDB() *bolt.DB {
	return db
}

func initDB() (db *bolt.DB) {
	db, err := bolt.Open(conf.CDR_DB_FILE, 0600, nil)
	if err != nil {
		log.Fatalln(err)
	}
	return
}

func init() {
	db = initDB()
	PutChan = make(chan gami.Message)
	DeleteChan = make(chan gami.Message)
}
