// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package downloader

import (
	"errors"

	"github.com/ethereum/go-ethereum/eth/protocols/snap"
)

// IsSyncCanceled reports whether err is the downloader's "syncing canceled
// (requested)" error, i.e. a sync cycle that was suspended on request (for
// example when the skeleton restarts on a newer head) rather than one that hit
// a real failure.
func IsSyncCanceled(err error) bool {
	return errors.Is(err, errCanceled)
}

// SnapProgress returns the progress of the snap state syncer the downloader
// actually drives. The exported SnapSyncer field is a separate snap/1 instance
// that never receives peers or work, so its Progress is always zero.
func (d *Downloader) SnapProgress() snap.Progress {
	return d.snapSyncer.Progress()
}
