// Copyright (c) 2016 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package fs

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/m3db/m3db/digest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTempFile(t *testing.T) *os.File {
	fd, err := ioutil.TempFile("", "testfile")
	require.NoError(t, err)
	return fd
}

func createTempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "foo")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func createDataFile(t *testing.T, shardDir string, blockStart time.Time, suffix string, b []byte) {
	filePath := filesetPathFromTime(shardDir, blockStart, suffix)
	createFile(t, filePath, b)
}

func createFile(t *testing.T, filePath string, b []byte) {
	fd, err := os.Create(filePath)
	require.NoError(t, err)
	if b != nil {
		fd.Write(b)
	}
	fd.Close()
}

func createCommitLogFiles(t *testing.T, iter, perSlot int) string {
	dir := createTempDir(t)
	commitLogsDir := path.Join(dir, commitLogsDirName)
	assert.NoError(t, os.Mkdir(commitLogsDir, 0755))
	for i := 0; i < iter; i++ {
		for j := 0; j < perSlot; j++ {
			filePath, _ := NextCommitLogsFile(dir, time.Unix(0, int64(i)))
			fd, err := os.Create(filePath)
			assert.NoError(t, err)
			assert.NoError(t, fd.Close())
		}
	}
	return dir
}

func TestOpenFilesFails(t *testing.T) {
	testFilePath := "/not/a/real/path"
	expectedErr := errors.New("synthetic error")

	opener := func(filePath string) (*os.File, error) {
		assert.Equal(t, filePath, testFilePath)
		return nil, expectedErr
	}

	var fd *os.File
	err := openFiles(opener, map[string]**os.File{
		testFilePath: &fd,
	})
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestCloseAllFails(t *testing.T) {
	file := createTempFile(t)
	defer os.Remove(file.Name())

	assert.NoError(t, file.Close())
	assert.Error(t, closeAll(file))
}

func TestByTimeAscending(t *testing.T) {
	files := []string{"foo/fileset-1-info.db", "foo/fileset-12-info.db", "foo/fileset-2-info.db"}
	expected := []string{"foo/fileset-1-info.db", "foo/fileset-2-info.db", "foo/fileset-12-info.db"}
	sort.Sort(byTimeAscending(files))
	require.Equal(t, expected, files)
}

func TestForEachInfoFile(t *testing.T) {
	dir := createTempDir(t)
	defer os.RemoveAll(dir)

	shard := uint32(0)
	shardDir := ShardDirPath(dir, shard)
	require.NoError(t, os.MkdirAll(shardDir, os.ModeDir|os.FileMode(0755)))

	blockStart := time.Unix(0, 0)
	buf := digest.NewBuffer()
	digest := digest.NewDigest()

	// No checkpoint file
	createDataFile(t, shardDir, blockStart, infoFileSuffix, nil)

	// No digest file
	blockStart = blockStart.Add(time.Nanosecond)
	createDataFile(t, shardDir, blockStart, infoFileSuffix, nil)
	createDataFile(t, shardDir, blockStart, checkpointFileSuffix, buf)

	// Digest of digest mismatch
	blockStart = blockStart.Add(time.Nanosecond)
	digests := []byte{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0xc}
	digest.Write(append(digests, 0xd))
	buf.WriteDigest(digest.Sum32())
	createDataFile(t, shardDir, blockStart, infoFileSuffix, nil)
	createDataFile(t, shardDir, blockStart, digestFileSuffix, digests)
	createDataFile(t, shardDir, blockStart, checkpointFileSuffix, buf)

	// Info file digest mismatch
	blockStart = blockStart.Add(time.Nanosecond)
	digest.Reset()
	digest.Write(digests)
	buf.WriteDigest(digest.Sum32())
	createDataFile(t, shardDir, blockStart, infoFileSuffix, []byte{0x1})
	createDataFile(t, shardDir, blockStart, digestFileSuffix, digests)
	createDataFile(t, shardDir, blockStart, checkpointFileSuffix, buf)

	// All digests match
	blockStart = blockStart.Add(time.Nanosecond)
	infoData := []byte{0x1, 0x2, 0x3, 0x4}
	digest.Reset()
	digest.Write(infoData)
	buf.WriteDigest(digest.Sum32())
	digestOfDigest := append(buf, []byte{0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0xc}...)
	digest.Reset()
	digest.Write(digestOfDigest)
	buf.WriteDigest(digest.Sum32())
	createDataFile(t, shardDir, blockStart, infoFileSuffix, infoData)
	createDataFile(t, shardDir, blockStart, digestFileSuffix, digestOfDigest)
	createDataFile(t, shardDir, blockStart, checkpointFileSuffix, buf)

	var fnames []string
	var res []byte
	ForEachInfoFile(dir, shard, testReaderBufferSize, func(fname string, data []byte) {
		fnames = append(fnames, fname)
		res = append(res, data...)
	})

	require.Equal(t, []string{filesetPathFromTime(shardDir, blockStart, infoFileSuffix)}, fnames)
	require.Equal(t, infoData, res)
}

func TestTimeFromName(t *testing.T) {
	_, err := TimeFromFileName("foo/bar")
	require.Error(t, err)
	require.Equal(t, "unexpected file name foo/bar", err.Error())

	_, err = TimeFromFileName("foo/bar-baz")
	require.Error(t, err)

	v, err := TimeFromFileName("foo-1-bar.db")
	expected := time.Unix(0, 1)
	require.Equal(t, expected, v)
	require.NoError(t, err)

	v, err = TimeFromFileName("foo/bar/foo-21234567890-bar.db")
	expected = time.Unix(0, 21234567890)
	require.Equal(t, expected, v)
	require.NoError(t, err)
}

func TestTimeAndIndexFromFileName(t *testing.T) {
	_, _, err := TimeAndIndexFromFileName("foo/bar")
	require.Error(t, err)
	require.Equal(t, "unexpected file name foo/bar", err.Error())

	_, _, err = TimeAndIndexFromFileName("foo/bar-baz")
	require.Error(t, err)

	type expected struct {
		t time.Time
		i int
	}
	ts, i, err := TimeAndIndexFromFileName("foo-1-0.db")
	exp := expected{time.Unix(0, 1), 0}
	require.Equal(t, exp.t, ts)
	require.Equal(t, exp.i, i)
	require.NoError(t, err)

	ts, i, err = TimeAndIndexFromFileName("foo/bar/foo-21234567890-1.db")
	exp = expected{time.Unix(0, 21234567890), 1}
	require.Equal(t, exp.t, ts)
	require.Equal(t, exp.i, i)
	require.NoError(t, err)
}

func TestFileExists(t *testing.T) {
	dir := createTempDir(t)
	defer os.RemoveAll(dir)

	shard := uint32(10)
	start := time.Now()
	shardDir := ShardDirPath(dir, shard)
	err := os.MkdirAll(shardDir, defaultNewDirectoryMode)
	require.NoError(t, err)

	infoFilePath := filesetPathFromTime(shardDir, start, infoFileSuffix)
	createDataFile(t, shardDir, start, infoFileSuffix, nil)
	require.True(t, FileExists(infoFilePath))
	require.False(t, FileExistsAt(shardDir, uint32(shard), start))

	checkpointFilePath := filesetPathFromTime(shardDir, start, checkpointFileSuffix)
	createDataFile(t, shardDir, start, checkpointFileSuffix, nil)
	require.True(t, FileExists(checkpointFilePath))
	require.False(t, FileExistsAt(shardDir, uint32(shard), start))

	os.Remove(infoFilePath)
	require.False(t, FileExists(infoFilePath))
}

func TestShardDirPath(t *testing.T) {
	require.Equal(t, "foo/bar/data/12", ShardDirPath("foo/bar", 12))
	require.Equal(t, "foo/bar/data/12", ShardDirPath("foo/bar/", 12))
}

func TestFilePathFromTime(t *testing.T) {
	start := time.Unix(1465501321, 123456789)
	inputs := []struct {
		prefix   string
		suffix   string
		expected string
	}{
		{"foo/bar", infoFileSuffix, "foo/bar/fileset-1465501321123456789-info.db"},
		{"foo/bar", indexFileSuffix, "foo/bar/fileset-1465501321123456789-index.db"},
		{"foo/bar", dataFileSuffix, "foo/bar/fileset-1465501321123456789-data.db"},
		{"foo/bar", checkpointFileSuffix, "foo/bar/fileset-1465501321123456789-checkpoint.db"},
		{"foo/bar/", infoFileSuffix, "foo/bar/fileset-1465501321123456789-info.db"},
	}
	for _, input := range inputs {
		require.Equal(t, input.expected, filesetPathFromTime(input.prefix, start, input.suffix))
	}
}

func TestCommitLogFiles(t *testing.T) {
	iter := 20
	perSlot := 3
	dir := createCommitLogFiles(t, iter, perSlot)
	defer os.RemoveAll(dir)

	createFile(t, path.Join(dir, "abcd"), nil)
	createFile(t, path.Join(dir, strconv.Itoa(perSlot+1)+fileSuffix), nil)
	createFile(t, path.Join(dir, strconv.Itoa(iter+1)+separator+strconv.Itoa(perSlot+1)+fileSuffix), nil)
	createFile(t, path.Join(dir, separator+strconv.Itoa(iter+1)+separator+strconv.Itoa(perSlot+1)+fileSuffix), nil)

	files, err := CommitLogFiles(CommitLogsDirPath(dir))
	require.NoError(t, err)
	require.Equal(t, iter*perSlot, len(files))
	for i := 0; i < iter; i++ {
		for j := 0; j < perSlot; j++ {
			entry := fmt.Sprintf("%d%s%d", i, separator, j)
			fileName := fmt.Sprintf("%s%s%s%s", commitLogFilePrefix, separator, entry, fileSuffix)

			x := (i * perSlot) + j
			require.Equal(t, path.Join(dir, commitLogsDirName, fileName), files[x])
		}
	}
}