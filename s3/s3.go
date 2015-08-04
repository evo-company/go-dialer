package s3

import (
	"fmt"
	"io/ioutil"
	"sync"

	"github.com/goamz/goamz/aws"
	"github.com/goamz/goamz/s3"

	"github.com/warik/go-dialer/conf"
)

var bucket *s3.Bucket
var once sync.Once

func Store(filePath, fileName string) error {
	once.Do(initS3)

	data, err := ioutil.ReadFile(fmt.Sprintf("%s_mp3/%s", filePath, fileName))
	if err != nil {
		return err
	}
	return bucket.Put(fileName, data, "audio/mpeg", s3.Private, s3.Options{})
}

func initS3() {
	accessKey := conf.GetConf().StorageSettings["accessKey"]
	secretKey := conf.GetConf().StorageSettings["secretKey"]
	s3Host := conf.GetConf().StorageSettings["s3Host"]
	baseBucket := conf.GetConf().StorageSettings["bucket"]
	dialerName := conf.GetConf().Name
	auth := aws.Auth{AccessKey: accessKey, SecretKey: secretKey}
	if dialerName == "" {
		dialerName = "main"
	}
	region := aws.Region{
		Name:       fmt.Sprintf("%s-dialer-calls", dialerName),
		S3Endpoint: s3Host,
	}
	client := s3.New(auth, region)
	bucket = client.Bucket(fmt.Sprintf("%s/%s", baseBucket, dialerName))
	err := bucket.PutBucket(s3.Private)
	if err != nil {
		panic(err)
	}
}
