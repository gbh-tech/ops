package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	prefix        = "reviewapps/"
	archivePrefix = "archive/"
	stateFile     = "state.json"
	manifestFile  = "manifest.json"
)

var (
	// ErrNotFound means the review app has no state.
	ErrNotFound = errors.New("review app state not found")
	// ErrExists means a create found state already there.
	ErrExists = errors.New("review app state already exists")
	// ErrConflict means another writer changed the state since it was read.
	ErrConflict = errors.New("review app state changed by another writer")
	// ErrLimit means the maximum number of review apps is running.
	ErrLimit = errors.New("review app limit reached")
	// ErrManifestMismatch means the manifest content does not match its recorded hash.
	ErrManifestMismatch = errors.New("review app manifest does not match its sha256")
)

// API is the subset of the S3 client the store uses.
type API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, in *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// Store reads and writes review app state in one bucket.
type Store struct {
	client API
	bucket string
	now    func() time.Time
}

// NewStore returns a store for the given bucket.
func NewStore(client API, bucket string) *Store {
	return &Store{client: client, bucket: bucket, now: time.Now}
}

// StateKey returns the key of a review app's state.
func StateKey(pr int) string {
	return prefix + ID(pr) + "/" + stateFile
}

// ManifestKey returns the key of a review app's manifest.
func ManifestKey(pr int) string {
	return prefix + ID(pr) + "/" + manifestFile
}

// ArchiveKey returns the key the final state is archived under.
func ArchiveKey(pr int, at time.Time) string {
	return archivePrefix + ID(pr) + "/" + at.UTC().Format("20060102T150405Z") + ".json"
}

// Get returns the state of a review app and its ETag, for a later Update.
func (s *Store) Get(ctx context.Context, pr int) (*State, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(StateKey(pr)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, "", fmt.Errorf("%w: %s", ErrNotFound, ID(pr))
		}
		return nil, "", fmt.Errorf("get state of %s: %w", ID(pr), err)
	}
	defer func() { _ = out.Body.Close() }()

	var st State
	if err := json.NewDecoder(out.Body).Decode(&st); err != nil {
		return nil, "", fmt.Errorf("decode state of %s: %w", ID(pr), err)
	}
	return &st, aws.ToString(out.ETag), nil
}

// Create writes the first state of a review app. It fails with ErrExists
// when the review app already has state.
func (s *Store) Create(ctx context.Context, st *State) (string, error) {
	etag, err := s.putState(ctx, st, func(in *s3.PutObjectInput) {
		in.IfNoneMatch = aws.String("*")
	})
	if isPreconditionFailed(err) {
		return "", fmt.Errorf("%w: %s", ErrExists, st.ID)
	}
	return etag, err
}

// Update replaces the state of a review app if it still has the given ETag.
// It fails with ErrConflict when another writer changed it first.
func (s *Store) Update(ctx context.Context, st *State, etag string) (string, error) {
	newETag, err := s.putState(ctx, st, func(in *s3.PutObjectInput) {
		in.IfMatch = aws.String(etag)
	})
	if isPreconditionFailed(err) {
		return "", fmt.Errorf("%w: %s", ErrConflict, st.ID)
	}
	return newETag, err
}

func (s *Store) putState(ctx context.Context, st *State, condition func(*s3.PutObjectInput)) (string, error) {
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode state of %s: %w", st.ID, err)
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(StateKey(st.PR)),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
	}
	condition(in)

	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		if isPreconditionFailed(err) {
			return "", err
		}
		return "", fmt.Errorf("put state of %s: %w", st.ID, err)
	}
	return aws.ToString(out.ETag), nil
}

// List returns the state of every review app, ordered by PR number.
func (s *Store) List(ctx context.Context) ([]*State, error) {
	keys, err := s.listKeys(ctx, prefix)
	if err != nil {
		return nil, err
	}

	var states []*State
	for _, key := range keys {
		pr, ok := prFromStateKey(key)
		if !ok {
			continue
		}
		st, _, err := s.Get(ctx, pr)
		if errors.Is(err, ErrNotFound) {
			// Destroyed between the list and the get.
			continue
		}
		if err != nil {
			return nil, err
		}
		states = append(states, st)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].PR < states[j].PR })
	return states, nil
}

// CountActive returns how many of the states count toward the limit.
func CountActive(states []*State) int {
	n := 0
	for _, st := range states {
		if st.Active() {
			n++
		}
	}
	return n
}

// CheckLimit fails with ErrLimit when limit review apps are already running.
func (s *Store) CheckLimit(ctx context.Context, limit int) error {
	states, err := s.List(ctx)
	if err != nil {
		return err
	}
	if n := CountActive(states); n >= limit {
		return fmt.Errorf("%w: %d of %d review apps are running, destroy one before creating another", ErrLimit, n, limit)
	}
	return nil
}

// PutManifest uploads the manifest of a review app and returns the S3
// version and hash the controller must read back.
func (s *Store) PutManifest(ctx context.Context, m *Manifest) (ManifestRef, error) {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return ManifestRef{}, fmt.Errorf("encode manifest of %s: %w", ID(m.PR), err)
	}
	out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(ManifestKey(m.PR)),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return ManifestRef{}, fmt.Errorf("put manifest of %s: %w", ID(m.PR), err)
	}
	return ManifestRef{VersionID: aws.ToString(out.VersionId), SHA256: digest(body)}, nil
}

// GetManifest reads the exact manifest version a run uploaded and checks its hash.
func (s *Store) GetManifest(ctx context.Context, pr int, ref ManifestRef) (*Manifest, error) {
	if ref.VersionID == "" || ref.SHA256 == "" {
		return nil, fmt.Errorf("%w: %s needs a version and a sha256", ErrManifestMismatch, ID(pr))
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(s.bucket),
		Key:       aws.String(ManifestKey(pr)),
		VersionId: aws.String(ref.VersionID),
	})
	if err != nil {
		return nil, fmt.Errorf("get manifest of %s version %s: %w", ID(pr), ref.VersionID, err)
	}
	defer func() { _ = out.Body.Close() }()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest of %s: %w", ID(pr), err)
	}
	if digest(body) != ref.SHA256 {
		return nil, fmt.Errorf("%w: %s version %s", ErrManifestMismatch, ID(pr), ref.VersionID)
	}

	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode manifest of %s: %w", ID(pr), err)
	}
	return &m, nil
}

// Archive copies the final state of a review app to archive/ and deletes
// everything under reviewapps/pr-<n>/. It returns the archive key.
func (s *Store) Archive(ctx context.Context, pr int) (string, error) {
	st, _, err := s.Get(ctx, pr)
	if err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode state of %s: %w", ID(pr), err)
	}

	key := ArchiveKey(pr, s.now())
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
	}); err != nil {
		return "", fmt.Errorf("archive state of %s: %w", ID(pr), err)
	}

	keys, err := s.listKeys(ctx, prefix+ID(pr)+"/")
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		return key, nil
	}
	objects := make([]s3types.ObjectIdentifier, 0, len(keys))
	for _, k := range keys {
		objects = append(objects, s3types.ObjectIdentifier{Key: aws.String(k)})
	}
	out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(s.bucket),
		Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("delete state of %s: %w", ID(pr), err)
	}
	if len(out.Errors) > 0 {
		e := out.Errors[0]
		return "", fmt.Errorf("delete %s: %s", aws.ToString(e.Key), aws.ToString(e.Message))
	}
	return key, nil
}

func (s *Store) listKeys(ctx context.Context, p string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(p),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", p, err)
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}
	return keys, nil
}

// prFromStateKey parses "reviewapps/pr-<n>/state.json".
func prFromStateKey(key string) (int, bool) {
	rest, ok := strings.CutPrefix(key, prefix+"pr-")
	if !ok {
		return 0, false
	}
	num, ok := strings.CutSuffix(rest, "/"+stateFile)
	if !ok {
		return 0, false
	}
	var pr int
	if _, err := fmt.Sscanf(num, "%d", &pr); err != nil || ID(pr) != "pr-"+num {
		return 0, false
	}
	return pr, true
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func isNotFound(err error) bool {
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchKey"
}

// isPreconditionFailed reports whether a conditional write lost: 412 when the
// condition did not hold, 409 when a concurrent conditional write won.
func isPreconditionFailed(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "PreconditionFailed", "ConditionalRequestConflict":
		return true
	}
	return false
}
