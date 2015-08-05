// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handle

import (
	"errors"
	"fmt"

	"github.com/googlecloudplatform/gcsfuse/fs/inode"
	"github.com/googlecloudplatform/gcsfuse/internal/gcsx"
	"github.com/jacobsa/gcloud/gcs"
	"golang.org/x/net/context"
)

type FileHandle struct {
	inode  *inode.FileInode
	bucket gcs.Bucket

	// A random reader configured to some (potentially previous) generation of
	// the object backing the inode, or nil.
	//
	// INVARIANT: If reader != nil, reader.CheckInvariants() doesn't panic.
	//
	// GUARDED_BY(inode)
	reader gcsx.RandomReader
}

func NewFileHandle(
	inode *inode.FileInode,
	bucket gcs.Bucket) (fh *FileHandle, err error) {
	err = errors.New("TODO")
	return
}

// Panic if any internal invariants are violated.
//
// LOCKS_REQUIRED(fh.inode)
func (fh *FileHandle) CheckInvariants() {
	// INVARIANT: If reader != nil, reader.CheckInvariants() doesn't panic.
	if fh.reader != nil {
		fh.reader.CheckInvariants()
	}
}

// Destroy any resources associated with the handle, which must not be used
// again.
func (fh *FileHandle) Destroy() {
	if fh.reader != nil {
		fh.reader.Destroy()
	}
}

// Return the inode backing this handle.
func (fh *FileHandle) Inode() *inode.FileInode {
	return fh.inode
}

// Equivalent to locking fh.Inode() and calling fh.Inode().Read, but may be
// more efficient.
//
// LOCKS_EXCLUDED(fh.inode)
func (fh *FileHandle) Read(
	ctx context.Context,
	dst []byte,
	offset int64) (n int, err error) {
	// Lock the inode and attempt to ensure that we have a reader for its current
	// state, or clear fh.reader if it's not possible to create one (probably
	// because the inode is dirty).
	fh.inode.Lock()
	err = fh.tryEnsureReader()
	if err != nil {
		fh.inode.Unlock()
		err = fmt.Errorf("tryEnsureReader: %v", err)
		return
	}

	// If we have an appropriate reader, unlock the inode and use that. This
	// allows reads to proceed concurrently with other operations; in particular,
	// multiple reads can run concurrently. It's safe because the user can't tell
	// if a concurrent write started during or after a read.
	if fh.reader != nil {
		fh.inode.Unlock()

		n, err = fh.reader.ReadAt(ctx, dst, offset)
		if err != nil {
			err = fmt.Errorf("fh.reader.ReadAt: %v", err)
			return
		}

		return
	}

	// Otherwise we must fall through to the inode.
	defer fh.inode.Unlock()
	n, err = fh.inode.Read(ctx, dst, offset)
	if err != nil {
		err = fmt.Errorf("fh.inode.Read: %v", err)
		return
	}

	return
}

// If possible, ensure that fh.reader is set to an appropriate random reader
// for the current state of the inode. Otherwise set it to nil.
//
// LOCKS_REQUIRED(fh.inode)
func (fh *FileHandle) tryEnsureReader() (err error) {
	// If the inode is dirty, there's nothing we can do. Throw away our reader if
	// we have one.
	if !fh.inode.SourceGenerationIsAuthoritative() {
		if fh.reader != nil {
			fh.reader.Destroy()
			fh.reader = nil
		}

		return
	}

	// If we already have a reader, and it's at the appropriate generation, we
	// can use it. Otherwise we must throw it away.
	if fh.reader != nil {
		if fh.reader.Object().Generation == fh.inode.SourceGeneration() {
			return
		}

		fh.reader.Destroy()
		fh.reader = nil
	}

	// Attempt to create an appropriate reader.
	rr, err := gcsx.NewRandomReader(fh.inode.Source(), fh.bucket)
	if err != nil {
		err = fmt.Errorf("NewRandomReader: %v", err)
		return
	}

	fh.reader = rr
	return
}
