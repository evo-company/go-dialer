package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/warik/dialer/conf"
)

var db *leveldb.DB

func GetDB() *leveldb.DB {
	return db
}

func GetStats() (result string) {
	t := []string{}
	statNames := []string{"stats", "sstables", "blockpool", "cachedblock", "openedtables",
		"alivesnaps", "aliveiters"}
	for _, statName := range statNames {
		statName = "leveldb." + statName
		data, err := db.GetProperty(statName)
		if err != nil {
			panic(err)
		}
		t = append(t, fmt.Sprintf("%v\n%v\n%v", statName, data, strings.Repeat("=", 20)))
	}
	return strings.Join(t, "\n\n")
}

func GetCount() (result int) {
	iter := db.NewIterator(nil, nil)
	defer iter.Release()
	for iter.Next() {
		result++
	}
	return
}

func initDB() (db *leveldb.DB) {
	path, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	db, err := leveldb.OpenFile(filepath.Join(path, conf.CDR_DB_FILE), nil)
	if err != nil {
		panic(err)
	}
	return
}

func OpenDB() {
	db = initDB()
}

func init() {
	OpenDB()
}
