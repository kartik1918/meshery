// Package store provides methods for interacting
// with a thread safe global store.
package store

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// globalStore is intentionally a global variable so
// that the life time of the variable is through
// out the program lifecycle
var globalStore *threadSafeStore

// Initialize initializes the global store
//
// It will skip initialization if the
// store has already been initialized once
func Initialize() {
	if globalStore == nil {
		globalStore = newThreadSafeStore()
	}
}

// Set will set the key value pairs in the global store
//
// If the key already exists but the data has changed then
// it will also be added to the list. It computes md5 hash of the data
// to check if the data already exists
func Set(key string, value interface{}) {
	globalStore.Lock()
	defer globalStore.Unlock()

	hash := md5Hash(value)

	if globalStore.store[key] == nil {
		globalStore.store[key] = map[string]interface{}{}
	}

	_, ok := globalStore.store[key][hash]
	if ok {
		return
	}

	globalStore.store[key][hash] = value
}

// GetAll returns all the values stored against the key
func GetAll(key string) []interface{} {
	globalStore.RLock()
	defer globalStore.RUnlock()

	res := []interface{}{}

	val, ok := globalStore.store[key]
	if !ok {
		return res
	}

	for _, v := range val {
		res = append(res, v)
	}

	return res
}

// PrefixMatch will return all the values which matches the given key
func PrefixMatch(key string) (res []interface{}) {
	globalStore.RLock()
	defer globalStore.RUnlock()

	for k, v := range globalStore.store {
		if strings.HasPrefix(k, key) {
			for _, el := range v {
				res = append(res, el)
			}
		}
	}

	return
}

// md5Hash takes in any value and returns md5 hash of it
func md5Hash(val interface{}) string {
	chash := md5.Sum(toBytes(val))

	return hex.EncodeToString(chash[:])
}

// toBytes takes in data and converts it into byte slice
//
// this function relies on json.Marshal for converting
// the data to []byte hence for custom types there must a
// custom marsheler implemented
func toBytes(data interface{}) []byte {
	res, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	return res
}
