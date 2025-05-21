package storage

import (
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	dbPath    = "dtc.db"
	bucketKey = "active_dtcs"
)

// OpenDB открывает (или создаёт) bbolt-базу и гарантирует наличие bucket’а.
func OpenDB(path string) (*bolt.DB, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}
	// Создаём bucket, если его нет
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketKey))
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// IsNew проверяет, встречался ли ранее код spn/fmi.
// Возвращает true и добавляет код, если он новый.
func IsNew(db *bolt.DB, spn uint32, fmi uint8) (bool, error) {
	key := []byte(fmt.Sprintf("%d:%d", spn, fmi))
	var isNew bool

	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketKey))
		if b.Get(key) == nil {
			// Ключа нет — это новый код
			isNew = true
			return b.Put(key, []byte{1})
		}
		// Уже был — игнорируем
		isNew = false
		return nil
	})
	return isNew, err
}

// Remove удаляет код spn/fmi (например, при получении PID 194I).
func Remove(db *bolt.DB, spn uint32, fmi uint8) error {
	key := []byte(fmt.Sprintf("%d:%d", spn, fmi))
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketKey))
		return b.Delete(key)
	})
}

// ClearAll сбрасывает все записи (например, после успешного PID 195→196).
func ClearAll(db *bolt.DB) error {
	return db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(bucketKey))
	})
}
