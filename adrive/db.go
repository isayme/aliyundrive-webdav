package adrive

import (
	"github.com/boltdb/bolt"
)

const dbFilePath = "./db.db"
const bucketName = "alipan"
const refreshTokenKey = "refreshToken"

func init() {
	err := createBucketIfNotExist()
	if err != nil {
		panic(err)
	}
}

func createBucketIfNotExist() error {
	db, err := bolt.Open(dbFilePath, 0600, nil)
	if err != nil {
		return err
	}

	defer db.Close()

	return db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})
}

func readRefreshToken() (string, error) {
	db, err := bolt.Open(dbFilePath, 0600, nil)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var refreshToken string

	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))

		v := b.Get([]byte(refreshTokenKey))
		if v != nil {
			refreshToken = string(v)
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return refreshToken, nil
}

func writeRefreshToken(refreshToken string) error {
	db, err := bolt.Open(dbFilePath, 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))

		return b.Put([]byte(refreshTokenKey), []byte(refreshToken))
	})
	return err
}
