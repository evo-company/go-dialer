package s3

import (
	"fmt"
	"os"

	"github.com/goamz/goamz/aws"
	"github.com/goamz/goamz/s3"

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
	return bucket.Put(fileName, data, "audio/mpeg", s3.Private, s3.Options{})
}

func init() {
	accessKey := conf.GetConf().StorageSettings["accessKey"]
	secretKey := conf.GetConf().StorageSettings["secretKey"]
	s3Host := conf.GetConf().StorageSettings["s3Host"]
	auth := aws.Auth{AccessKey: accessKey, SecretKey: secretKey}
	region := aws.Region{
		Name:       fmt.Sprintf("%s-dialer-calls", conf.GetConf().Name),
		S3Endpoint: s3Host,
	}
	client := s3.New(auth, region)
	dialerName := conf.GetConf().Name
	if dialerName == "" {
		dialerName = "main"
	}
	bucket = client.Bucket(fmt.Sprintf("s3://%s/calls", dialerName))
}
