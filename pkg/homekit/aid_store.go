package homekit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// aidStore keeps the accessory id (AID) of every exposed path. The Home app
// ties rooms, names and automations to the AID, so an object must keep its id
// across restarts, and an id is never given to another object, even after
// its object is gone.
type aidStore struct {
	mu    sync.Mutex
	file  string
	Next  uint64            `json:"next"`
	IDs   map[string]uint64 `json:"ids"`
	dirty bool
}

// firstAID: 1 belongs to the bridge.
const firstAID = 2

// recoveredAID is where numbering continues when the saved ids are lost: high
// enough not to hand an id the Home app still remembers to another object.
const recoveredAID = 10000

func loadAIDStore(dataDir string) *aidStore {
	st := &aidStore{file: filepath.Join(dataDir, "homekit-aids.json"), Next: firstAID, IDs: map[string]uint64{}}
	if dataDir == "" {
		st.file = ""
		return st
	}
	data, err := os.ReadFile(st.file)
	if err != nil {
		if !os.IsNotExist(err) {
			st.Next = recoveryBase(st.file, nil)
			log.Printf("[HomeKit] accessory ids unreadable (%v); numbering from %d", err, st.Next)
		}
		return st
	}
	var saved aidStore
	if err := json.Unmarshal(data, &saved); err != nil || saved.IDs == nil {
		// Keep the broken file for inspection; new ids start above anything it
		// (or an earlier broken file) may hold, so no remembered id lands on
		// another object
		st.Next = recoveryBase(st.file, data)
		bad := fmt.Sprintf("%s.bad-%d", st.file, time.Now().Unix())
		if rerr := os.Rename(st.file, bad); rerr != nil {
			log.Printf("[HomeKit] accessory ids unreadable (%v), and not moved aside: %v", err, rerr)
		} else {
			log.Printf("[HomeKit] accessory ids unreadable (%v), moved to %s; numbering from %d", err, bad, st.Next)
		}
		st.dirty = true
		return st
	}
	st.IDs = saved.IDs
	st.Next = saved.Next
	for _, id := range st.IDs {
		if id >= st.Next {
			st.Next = id + 1
		}
	}
	if st.Next < firstAID {
		st.Next = firstAID
	}
	return st
}

// recoveryBase is the first id to give after the id file was lost: above
// recoveredAID and above every number in the broken content and in earlier
// broken files.
func recoveryBase(file string, broken []byte) uint64 {
	next := uint64(recoveredAID)
	scan := func(b []byte) {
		// only JSON values (`: 123`), not digits inside paths like CLU212345678;
		// ids stay small enough for every Home app version
		for _, m := range idValueRe.FindAllSubmatch(b, -1) {
			if n, err := strconv.ParseUint(string(m[1]), 10, 64); err == nil && n < 1<<31 && n+1 > next {
				next = n + 1
			}
		}
	}
	scan(broken)
	if old, _ := filepath.Glob(file + ".bad*"); old != nil {
		for _, f := range old {
			if b, err := os.ReadFile(f); err == nil {
				scan(b)
			}
		}
	}
	return next
}

var idValueRe = regexp.MustCompile(`:\s*([0-9]+)`)

// known returns the path's saved AID, if any.
func (st *aidStore) known(path string) (uint64, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id, ok := st.IDs[path]
	return id, ok
}

// id returns the path's AID, giving it the next free one the first time.
func (st *aidStore) id(path string) uint64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	if id, ok := st.IDs[path]; ok {
		return id
	}
	id := st.Next
	st.Next++
	st.IDs[path] = id
	st.dirty = true
	return id
}

// save writes new ids atomically (temp file + rename).
func (st *aidStore) save() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.dirty || st.file == "" {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(st.file), ".homekit-aids-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), st.file); err != nil {
		return err
	}
	// The rename itself must survive a power cut too
	if dir, err := os.Open(filepath.Dir(st.file)); err == nil {
		dir.Sync()
		dir.Close()
	}
	st.dirty = false
	return nil
}
