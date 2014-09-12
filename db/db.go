package db

import (
	"log"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/warik/dialer/conf"
)

type innerDB struct {
	DB *bolt.DB
	Rw *sync.RWMutex
}

var db *innerDB

func GetDB() *innerDB {
	return db
}

func (idb *innerDB) Read(f func(tx *bolt.Tx) error) error {
	(*idb).Rw.Lock()
	defer (*idb).Rw.Unlock()
	return (*idb).DB.View(f)
}

func (idb *innerDB) Update(f func(tx *bolt.Tx) error) error {
	(*idb).Rw.Lock()
	defer (*idb).Rw.Unlock()
	return (*idb).DB.Update(f)
}

func (idb *innerDB) Close() {
	(*idb).Rw.Lock()
	defer (*idb).Rw.Unlock()
	(*idb).DB.Close()
}

func initDB() (db *bolt.DB) {
	db, err := bolt.Open(conf.CDR_DB_FILE, 0600, nil)
	if err != nil {
		log.Fatalln(err)
	}
	return
}

func init() {
	db = &innerDB{DB: initDB(), Rw: &sync.RWMutex{}}
}
