// Copyright 2017 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package tikv

import (
	"fmt"
	"time"

	"github.com/ngaut/log"
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb"
	"github.com/pingcap/tidb/kv"
)

type testSafePointSuite struct {
	store    *tikvStore
	oracle   *mockOracle
	gcWorker *GCWorker
	prefix   string
}

var _ = Suite(&testSafePointSuite{})

func (s *testSafePointSuite) SetUpSuite(c *C) {
	s.store = newTestStore(c)
	s.oracle = &mockOracle{}
	s.store.oracle = s.oracle
	_, err := tidb.BootstrapSession(s.store)
	c.Assert(err, IsNil)
	gcWorker, err := NewGCWorker(s.store, false)
	c.Assert(err, IsNil)
	s.gcWorker = gcWorker
	s.prefix = fmt.Sprintf("seek_%d", time.Now().Unix())
}

func (s *testSafePointSuite) TearDownSuite(c *C) {
	err := s.store.Close()
	c.Assert(err, IsNil)
}

func (s *testSafePointSuite) beginTxn(c *C) *tikvTxn {
	txn, err := s.store.Begin()
	c.Assert(err, IsNil)
	return txn.(*tikvTxn)
}

func mymakeKeys(rowNum int, prefix string) []kv.Key {
	keys := make([]kv.Key, 0, rowNum)
	for i := 0; i < rowNum; i++ {
		k := encodeKey(prefix, s08d("key", i))
		keys = append(keys, k)
	}
	return keys
}

func (s *testSafePointSuite) waitUntilErrorPlugIn(t uint64) {
	for {
		s.gcWorker.saveSafePoint(gcSavedSafePoint, t+10)
		cachedTime := time.Now()
		newSafePoint, err := s.gcWorker.loadSafePoint(gcSavedSafePoint)
		if err == nil {
			s.store.UpdateSPCache(newSafePoint, cachedTime)
			break
		} else {
			log.Error(err)
			time.Sleep(time.Second)
		}
	}
}

func (s *testSafePointSuite) TestSafePoint(c *C) {
	txn := s.beginTxn(c)
	for i := 0; i < 10; i++ {
		err := txn.Set(encodeKey(s.prefix, s08d("key", i)), valueBytes(i))
		c.Assert(err, IsNil)
	}
	err := txn.Commit()
	c.Assert(err, IsNil)

	// for txn get
	txn2 := s.beginTxn(c)
	_, err = txn2.Get(encodeKey(s.prefix, s08d("key", 0)))
	c.Assert(err, IsNil)

	s.waitUntilErrorPlugIn(txn2.startTS)

	_, geterr2 := txn2.Get(encodeKey(s.prefix, s08d("key", 0)))
	c.Assert(geterr2, NotNil)

	// for txn seek
	txn3 := s.beginTxn(c)

	s.waitUntilErrorPlugIn(txn3.startTS)

	_, seekerr := txn3.Seek(encodeKey(s.prefix, ""))
	c.Assert(seekerr, NotNil)

	// for snapshot batchGet
	keys := mymakeKeys(10, s.prefix)
	txn4 := s.beginTxn(c)

	s.waitUntilErrorPlugIn(txn4.startTS)

	snapshot := newTiKVSnapshot(s.store, kv.Version{Ver: txn4.StartTS()})
	_, batchgeterr := snapshot.BatchGet(keys)
	c.Assert(batchgeterr, NotNil)
}
