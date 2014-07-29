package db

import (
	"encoding/binary"
	_ "log"
)

//
// ## Sets
// set member
//   Set + (key length as binary encoded uint32) + set key + set member  => empty
// set cardinality
//   Meta + SetCardinality + setkey => binary encoded uint32
// the total number of set
//   Meta + SetCnt => binary encoded uint32
//

// Format a set member
func keySetMember(set []byte, key interface{}) []byte {
	var keybyte []byte
	switch k := key.(type) {
	case []byte:
		keybyte = k
	case string:
		keybyte = []byte(k)
	case byte:
		keybyte = []byte{k}
	}
	k := make([]byte, len(keybyte)+len(set)+5)
	k[0] = Set
	binary.LittleEndian.PutUint32(k[1:5], uint32(len(set)))
	cpos := 5 + len(set)
	copy(k[5:cpos], set)
	if len(keybyte) > 0 {
		copy(k[cpos:], keybyte)
	}
	return k
}

func decodeKeySetMember(key []byte) []byte {
	// The first byte is already remove
	cpos := int(binary.LittleEndian.Uint32(key[0:4]))+4
	member := make([]byte, len(key)-cpos)
	copy(member[:], key[cpos:])
	return member
}

func keySetCard(key []byte) []byte {
	cardkey := make([]byte, len(key)+1)
	cardkey[0] = SetCardinality
	copy(cardkey[1:], key)
	return cardkey
}

//func (db *DB) SetCnt() int {}

//   Set + (key length as binary encoded uint32) + set key + set member  => empty
func (db *DB) Scard(key string) (int, error) {
	bkey := []byte(key)
	cardkey := keySetCard(bkey)
	card, err := db.getUint32(cardkey)
	return int(card), err
}

func (db *DB) Sadd(key string, members ...string) (int, error) {
	bkey := []byte(key)
	cnt := 0
	// Init the set
	if err := db.put(keySetMember(bkey, []byte{}), []byte{}); err != nil {
		return 0, err
	}
	for _, member := range members {
		kmember := keySetMember(bkey, member)
		cval, err := db.get(kmember)
		if err != nil {
			return 0, err
		}
		if cval == nil {
			if err := db.put(kmember, []byte{}); err != nil {
				return 0, err
			}
			cnt++
		}
	}
	cardkey := keySetCard(bkey)
	db.incrUint32(cardkey, cnt)
	return cnt, nil
}

func (db *DB) Sismember(key, member string) int {
	bkey := []byte(key)
	cval, _ := db.get(keySetMember(bkey, member))
	cnt := 0
	if cval != nil {
		cnt++
	}
	return cnt
}

func (db *DB) Smembers(key string) [][]byte {
	bkey := []byte(key)
	start := keySetMember(bkey, []byte{})
	end := keySetMember(bkey, "\xff")
	kvs, _ := GetRange(db.db, start, end, 0)
	res := [][]byte{}
	for _, kv := range kvs {
		member := decodeKeySetMember([]byte(kv.Key))
		if string(member) != "" {
			res = append(res, member)
		}
	}
	return res
}

// Remove the set
func (db *DB) Sdel(key string) error {
	bkey := []byte(key)
	start := keySetMember(bkey, []byte{})
	end := keySetMember(bkey, "\xff")
	kvs, _ := GetRange(db.db, start, end, 0)
	for _, kv := range kvs {
		db.del([]byte(kv.Key))
	}
	cardkey := keySetCard(bkey)
	db.del(cardkey)
	return nil
}

// func (db *DB) Srange(snapId, kStart string, kEnd string, limit int) [][]byte
