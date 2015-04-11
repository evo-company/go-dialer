package s3

import (
	"fmt"
	"os"

	"github.com/mitchellh/goamz/aws"
	"github.com/mitchellh/goamz/s3"

	"github.com/warik/go-dialer/conf"
)

var bucket *s3.Bucket

func Store(filePath, fileName string) error {
	var data []byte
	file, err := os.Open(fmt.Sprint("%s/mp3/%s", filePath, fileName))
	if err != nil {
		return err
	}
	if _, err := file.Read(data); err != nil {
		return err
	}
	return bucket.Put("", data, "audio/mpeg", s3.Private)
}

func init() {
	accessKey := conf.GetConf().StorageSettings["accessKey"]
	secretKey := conf.GetConf().StorageSettings["secretKey"]
	bucketName := conf.GetConf().StorageSettings["bucketName"]
	auth, err := aws.GetAuth(accessKey, secretKey)
	if err != nil {
		panic(err)
	}
	client := s3.New(auth, aws.EUCentral)
	bucket = client.Bucket(bucketName)
}
