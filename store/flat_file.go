package store

import (
	"io"
	"os"
	"path"
	"strings"

	"github.com/kr/fs"

	"sourcegraph.com/sourcegraph/rwvfs"
	"sourcegraph.com/sourcegraph/srclib/graph"
	"sourcegraph.com/sourcegraph/srclib/unit"
)

// FlatFileConfig configures a flat file store.
type FlatFileConfig struct {
	Codec Codec
}

const SrclibStoreDir = ".srclib-store"

// A flatFileMultiRepoStore is a MultiRepoStore that stores data in
// flat files.
type flatFileMultiRepoStore struct {
	fs rwvfs.FileSystem
	repoStores
	codec Codec
}

// NewFlatFileMultiRepoStore creates a new repository store (that can
// be imported into) that is backed by files on a filesystem.
//
// The repoPathFunc takes a repo ID (URI) and returns the
// slash-delimited subpath where its data should be stored in the
// multi-repo store. If nil, it defaults to a function that returns
// the string passed in plus "/.srclib-store".
//
// The isRepoPathFunc takes a subpath and returns true if it is a repo
// path returned by repoPathFunc. This is used when listing all repos
// (in the Repos method). Repos may not be nested, so if
// isRepoPathFunc returns true for a dir, it is not recursively
// searched for repos. If nil, it defaults to returning true if the
// last path component is ".srclib-store" (which means it works with
// the default repoPathFunc).
func NewFlatFileMultiRepoStore(fs rwvfs.FileSystem, conf *FlatFileConfig) MultiRepoStoreImporter {
	if conf == nil {
		conf = &FlatFileConfig{}
	}
	if conf.Codec == nil {
		conf.Codec = JSONCodec{}
	}

	setCreateParentDirs(fs)
	mrs := &flatFileMultiRepoStore{fs: fs, codec: conf.Codec}
	mrs.repoStores = repoStores{mrs}
	return mrs
}

func (s *flatFileMultiRepoStore) Repo(repo string) (string, error) {
	repos, err := s.Repos(ByRepo(repo))
	if err != nil {
		return "", err
	}
	if len(repos) == 0 {
		return "", errRepoNotExist
	}
	return repos[0], nil
}

func (s *flatFileMultiRepoStore) Repos(f ...RepoFilter) ([]string, error) {
	var repos []string
	w := fs.WalkFS(".", rwvfs.Walkable(s.fs))
	for w.Step() {
		if err := w.Err(); err != nil {
			return nil, err
		}
		fi := w.Stat()
		if fi.Mode().IsDir() {
			if fi.Name() == SrclibStoreDir {
				w.SkipDir()
				repo := path.Dir(w.Path())
				if repoFilters(f).SelectRepo(repo) {
					repos = append(repos, repo)
				}
				continue
			}
			if strings.HasPrefix(fi.Name(), ".") {
				w.SkipDir()
				continue
			}
		}
	}
	return repos, nil
}

func (s *flatFileMultiRepoStore) openRepoStore(repo string) (RepoStore, error) {
	return NewFlatFileRepoStore(rwvfs.Sub(s.fs, path.Join(repo, SrclibStoreDir)), &FlatFileConfig{Codec: s.codec}), nil
}

func (s *flatFileMultiRepoStore) openAllRepoStores() (map[string]RepoStore, error) {
	repos, err := s.Repos()
	if err != nil {
		return nil, err
	}

	rss := make(map[string]RepoStore, len(repos))
	for _, repo := range repos {
		var err error
		rss[repo], err = s.openRepoStore(repo)
		if err != nil {
			return nil, err
		}
	}
	return rss, nil
}

var _ repoStoreOpener = (*flatFileMultiRepoStore)(nil)

func (s *flatFileMultiRepoStore) Import(repo, commitID string, unit *unit.SourceUnit, data graph.Output) error {
	if unit != nil {
		cleanForImport(&data, repo, unit.Type, unit.Name)
	}
	repoPath := path.Join(repo, SrclibStoreDir)
	if err := rwvfs.MkdirAll(s.fs, repoPath); err != nil && !os.IsExist(err) {
		return err
	}
	rs := NewFlatFileRepoStore(rwvfs.Sub(s.fs, repoPath), &FlatFileConfig{Codec: s.codec})
	return rs.Import(commitID, unit, data)
}

func (s *flatFileMultiRepoStore) String() string { return "flatFileMultiRepoStore" }

// A flatFileRepoStore is a RepoStore that stores data in flat files.
type flatFileRepoStore struct {
	fs rwvfs.FileSystem
	treeStores

	codec Codec
}

// NewFlatFileRepoStore creates a new repository store (that can be
// imported into) that is backed by files on a filesystem.
func NewFlatFileRepoStore(fs rwvfs.FileSystem, conf *FlatFileConfig) RepoStoreImporter {
	if conf == nil {
		conf = &FlatFileConfig{}
	}
	if conf.Codec == nil {
		conf.Codec = JSONCodec{}
	}

	setCreateParentDirs(fs)
	rs := &flatFileRepoStore{fs: fs, codec: conf.Codec}
	rs.treeStores = treeStores{rs}
	return rs
}

func (s *flatFileRepoStore) Version(key VersionKey) (*Version, error) {
	versions, err := s.Versions(ByCommitID(key.CommitID))
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, errVersionNotExist
	}
	return versions[0], nil
}

func (s *flatFileRepoStore) Versions(f ...VersionFilter) ([]*Version, error) {
	versionDirs, err := s.versionDirs()
	if err != nil {
		return nil, err
	}

	var versions []*Version
	for _, dir := range versionDirs {
		version := &Version{CommitID: path.Base(dir)}
		if versionFilters(f).SelectVersion(version) {
			versions = append(versions, version)
		}
	}
	return versions, nil
}

func (s *flatFileRepoStore) versionDirs() ([]string, error) {
	entries, err := s.fs.ReadDir(".")
	if err != nil {
		return nil, err
	}
	dirs := make([]string, len(entries))
	for i, e := range entries {
		dirs[i] = e.Name()
	}
	return dirs, nil
}

func (s *flatFileRepoStore) Import(commitID string, unit *unit.SourceUnit, data graph.Output) error {
	if unit != nil {
		cleanForImport(&data, "", unit.Type, unit.Name)
	}
	ts := s.newTreeStore(commitID)
	if err := ts.fs.Mkdir("."); err != nil && !os.IsExist(err) {
		return err
	}
	return ts.Import(unit, data)
}

func (s *flatFileRepoStore) newTreeStore(commitID string) *flatFileTreeStore {
	return newFlatFileTreeStore(rwvfs.Sub(s.fs, commitID), &FlatFileConfig{Codec: s.codec})
}

func (s *flatFileRepoStore) openTreeStore(commitID string) (TreeStore, error) {
	return s.newTreeStore(commitID), nil
}

func (s *flatFileRepoStore) openAllTreeStores() (map[string]TreeStore, error) {
	versionDirs, err := s.versionDirs()
	if err != nil {
		return nil, err
	}

	tss := make(map[string]TreeStore, len(versionDirs))
	for _, dir := range versionDirs {
		commitID := path.Base(dir)
		tss[commitID] = newFlatFileTreeStore(rwvfs.Sub(s.fs, commitID), &FlatFileConfig{Codec: s.codec})
	}
	return tss, nil
}

var _ treeStoreOpener = (*flatFileRepoStore)(nil)

func (s *flatFileRepoStore) String() string { return "flatFileRepoStore" }

// A flatFileTreeStore is a TreeStore that stores data in flat files
// in a filesystem.
type flatFileTreeStore struct {
	fs rwvfs.FileSystem
	unitStores

	codec Codec
}

func newFlatFileTreeStore(fs rwvfs.FileSystem, conf *FlatFileConfig) *flatFileTreeStore {
	if conf == nil {
		conf = &FlatFileConfig{}
	}
	if conf.Codec == nil {
		conf.Codec = JSONCodec{}
	}

	ts := &flatFileTreeStore{fs: fs, codec: conf.Codec}
	ts.unitStores = unitStores{ts}
	return ts
}

func (s *flatFileTreeStore) Unit(key unit.Key) (*unit.SourceUnit, error) {
	units, err := s.Units(ByUnit(key.UnitType, key.Unit))
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, errUnitNotExist
	}
	return units[0], nil
}

func (s *flatFileTreeStore) Units(f ...UnitFilter) ([]*unit.SourceUnit, error) {
	unitFilenames, err := s.unitFilenames()
	if err != nil {
		return nil, err
	}

	var units []*unit.SourceUnit
	for _, filename := range unitFilenames {
		unit, err := s.openUnitFile(filename)
		if err != nil {
			return nil, err
		}
		if unitFilters(f).SelectUnit(unit) {
			units = append(units, unit)
		}
	}
	return units, nil
}

func (s *flatFileTreeStore) openUnitFile(filename string) (*unit.SourceUnit, error) {
	f, err := s.fs.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errUnitNotExist
		}
		return nil, err
	}
	defer func() {
		err2 := f.Close()
		if err == nil {
			err = err2
		}
	}()

	var unit unit.SourceUnit
	return &unit, s.codec.Decode(f, &unit)
}

func (s *flatFileTreeStore) unitFilenames() ([]string, error) {
	var files []string
	w := fs.WalkFS(".", rwvfs.Walkable(s.fs))
	for w.Step() {
		if err := w.Err(); err != nil {
			return nil, err
		}
		fi := w.Stat()
		if fi.Mode().IsRegular() && strings.HasSuffix(fi.Name(), unitFileSuffix) {
			files = append(files, w.Path())
		}
	}
	return files, nil
}

func (s *flatFileTreeStore) unitFilename(unitType, unit string) string {
	return path.Join(unit, unitType+unitFileSuffix)
}

const unitFileSuffix = ".unit.json"

func (s *flatFileTreeStore) Import(unit *unit.SourceUnit, data graph.Output) (err error) {
	if unit == nil {
		return rwvfs.MkdirAll(s.fs, ".")
	}

	unitFilename := s.unitFilename(unit.Type, unit.Name)
	if err := rwvfs.MkdirAll(s.fs, path.Dir(unitFilename)); err != nil {
		return err
	}
	f, err := s.fs.Create(unitFilename)
	if err != nil {
		return err
	}
	defer func() {
		err2 := f.Close()
		if err == nil {
			err = err2
		}
	}()
	if err := s.codec.Encode(f, unit); err != nil {
		return err
	}

	dir := strings.TrimSuffix(unitFilename, unitFileSuffix)
	if err := rwvfs.MkdirAll(s.fs, dir); err != nil {
		return err
	}
	us, err := s.openUnitStore(unitID{unitType: unit.Type, unit: unit.Name})
	if err != nil {
		return err
	}
	cleanForImport(&data, "", unit.Type, unit.Name)
	return us.(UnitStoreImporter).Import(data)
}

// useIndexedUnitStore indicates whether the indexedUnitStore should
// be used to access data (defs, refs, etc.) in source units. If it's
// false, the flat-file unit store is used (which requires full scans
// for all filters).
var useIndexedUnitStore = true

func (s *flatFileTreeStore) openUnitStore(u unitID) (UnitStore, error) {
	filename := s.unitFilename(u.unitType, u.unit)
	dir := strings.TrimSuffix(filename, unitFileSuffix)
	if useIndexedUnitStore {
		return newIndexedUnitStore(rwvfs.Sub(s.fs, dir), s.codec), nil
	}
	return &flatFileUnitStore{fs: rwvfs.Sub(s.fs, dir), codec: s.codec}, nil
}

func (s *flatFileTreeStore) openAllUnitStores() (map[unitID]UnitStore, error) {
	unitFiles, err := s.unitFilenames()
	if err != nil {
		return nil, err
	}

	uss := make(map[unitID]UnitStore, len(unitFiles))
	for _, unitFile := range unitFiles {
		// TODO(sqs): duplicated code both here and in openUnitStore
		// for "dir" and "u".
		dir := strings.TrimSuffix(unitFile, unitFileSuffix)
		u := unitID{unitType: path.Base(dir), unit: path.Dir(dir)}
		var err error
		uss[u], err = s.openUnitStore(u)
		if err != nil {
			return nil, err
		}
	}
	return uss, nil
}

var _ unitStoreOpener = (*flatFileTreeStore)(nil)

func (s *flatFileTreeStore) String() string { return "flatFileTreeStore" }

// A flatFileUnitStore is a UnitStore that stores data in flat files
// in a filesystem.
//
// It is typically wrapped by an indexedUnitStore, which provides fast
// responses to indexed queries and passes non-indexed queries through
// to this underlying flatFileUnitStore.
type flatFileUnitStore struct {
	// fs is the filesystem where data (and indexes, if
	// flatFileUnitStore is wrapped by an indexedUnitStore) are
	// written to and read from. The store may create multiple files
	// and arbitrary directory trees in fs (for indexes, etc.).
	fs rwvfs.FileSystem

	codec Codec
}

const (
	unitDefsFilename = "def.dat"
	unitRefsFilename = "ref.dat"
)

func (s *flatFileUnitStore) Def(key graph.DefKey) (*graph.Def, error) {
	if err := checkDefKeyValidForUnitStore(key); err != nil {
		return nil, err
	}

	defs, err := s.Defs(defPathFilter(key.Path))
	if err != nil {
		return nil, err
	}
	if len(defs) == 0 {
		return nil, errDefNotExist
	}
	return defs[0], nil
}

func (s *flatFileUnitStore) Defs(fs ...DefFilter) (defs []*graph.Def, err error) {
	f, err := s.fs.Open(unitDefsFilename)
	if err != nil {
		return nil, err
	}
	defer func() {
		err2 := f.Close()
		if err == nil {
			err = err2
		}
	}()

	dec := newDecoder(s.codec, f)
	for {
		var def *graph.Def
		if err := dec.Decode(&def); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if defFilters(fs).SelectDef(def) {
			defs = append(defs, def)
		}
	}
	return defs, nil
}

func (s *flatFileUnitStore) Refs(fs ...RefFilter) (refs []*graph.Ref, err error) {
	f, err := s.fs.Open(unitRefsFilename)
	if err != nil {
		return nil, err
	}
	defer func() {
		err2 := f.Close()
		if err == nil {
			err = err2
		}
	}()

	dec := newDecoder(s.codec, f)
	for {
		var ref *graph.Ref
		if err := dec.Decode(&ref); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if refFilters(fs).SelectRef(ref) {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s *flatFileUnitStore) Import(data graph.Output) error {
	cleanForImport(&data, "", "", "")
	if _, err := s.writeDefs(&data); err != nil {
		return err
	}
	if _, err := s.writeRefs(&data); err != nil {
		return err
	}
	return nil
}

// writeDefs writes the def data file. It also tracks (in ofs) the
// serialized byte offset where each def's serialized representation
// begins (which is used during index construction).
func (s *flatFileUnitStore) writeDefs(data *graph.Output) (ofs byteOffsets, err error) {
	f, err := s.fs.Create(unitDefsFilename)
	if err != nil {
		return nil, err
	}
	defer func() {
		err2 := f.Close()
		if err == nil {
			err = err2
		}
	}()

	cw := &countingWriter{Writer: f}
	ofs = make(byteOffsets, len(data.Defs))
	for i, def := range data.Defs {
		ofs[i] = cw.n
		if err := s.codec.Encode(cw, def); err != nil {
			return nil, err
		}
	}
	return ofs, nil
}

// writeDefs writes the ref data file.
func (s *flatFileUnitStore) writeRefs(data *graph.Output) (ofs byteOffsets, err error) {
	f, err := s.fs.Create(unitRefsFilename)
	if err != nil {
		return nil, err
	}
	defer func() {
		err2 := f.Close()
		if err == nil {
			err = err2
		}
	}()

	cw := &countingWriter{Writer: f}
	ofs = make(byteOffsets, len(data.Refs))
	for i, ref := range data.Refs {
		ofs[i] = cw.n
		if err := s.codec.Encode(cw, ref); err != nil {
			return nil, err
		}
	}
	return ofs, nil
}

func (s *flatFileUnitStore) String() string { return "flatFileUnitStore" }

// countingWriter wraps an io.Writer, counting the number of bytes
// write.
type countingWriter struct {
	io.Writer
	n int64
}

func (cr *countingWriter) Write(p []byte) (n int, err error) {
	n, err = cr.Writer.Write(p)
	cr.n += int64(n)
	return
}

func setCreateParentDirs(fs rwvfs.FileSystem) {
	type createParents interface {
		CreateParentDirs(bool)
	}
	if fs, ok := fs.(createParents); ok {
		fs.CreateParentDirs(true)
	}
}