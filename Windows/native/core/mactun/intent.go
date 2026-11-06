package mactun

import (
	"encoding/json"
	"errors"
	"os"
	"path"

	"bluegrass/native/core/mactunipc"
)

type intentFiles interface {
	Read(string) ([]byte, error)
	Write(string, []byte) error // atomic and durable, including parent directory
	Remove(string) error
	SyncDirectory(string) error
}

// The discovery marker must share IPC's stable location. RuntimeDirectory is
// volatile across reboot and contains only live transport state, not intent.
func persistIntent(owner uint32, active bool, files intentFiles) error {
	if owner == 0 {
		return ErrOwner
	}
	target := mactunipc.IntentPath
	raw, err := files.Read(target)
	absent := errors.Is(err, os.ErrNotExist)
	if err != nil && !absent {
		return err
	}
	if !absent {
		current, e := mactunipc.DecodeIntent(raw)
		if e != nil {
			return e
		}
		if current.OwnerUID != owner {
			return ErrOwner
		}
	}
	if !active {
		if absent {
			return nil
		}
		if err = files.Remove(target); err != nil {
			return err
		}
		return files.SyncDirectory(path.Dir(target))
	}
	raw, _ = json.Marshal(mactunipc.Intent{Version: 1, OwnerUID: owner, Active: true, RecoveryRequired: true})
	return files.Write(target, raw)
}
