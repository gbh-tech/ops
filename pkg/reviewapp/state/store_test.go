package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// fakeS3 is an in-memory versioned bucket that honors conditional writes.
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string][]version // newest last
	next     int
	pageSize int32
}

type version struct {
	id   string
	etag string
	body []byte
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: map[string][]version{}, pageSize: 1000}
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	versions := f.objects[aws.ToString(in.Key)]
	if len(versions) == 0 {
		return nil, &s3types.NoSuchKey{}
	}
	v := versions[len(versions)-1]
	if in.VersionId != nil {
		found := false
		for _, candidate := range versions {
			if candidate.id == aws.ToString(in.VersionId) {
				v, found = candidate, true
			}
		}
		if !found {
			return nil, &smithy.GenericAPIError{Code: "NoSuchVersion"}
		}
	}
	return &s3.GetObjectOutput{
		Body:      io.NopCloser(bytes.NewReader(v.body)),
		ETag:      aws.String(v.etag),
		VersionId: aws.String(v.id),
	}, nil
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := aws.ToString(in.Key)
	versions := f.objects[key]
	if in.IfNoneMatch != nil && len(versions) > 0 {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	if in.IfMatch != nil && (len(versions) == 0 || versions[len(versions)-1].etag != aws.ToString(in.IfMatch)) {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	body, _ := io.ReadAll(in.Body)
	f.next++
	v := version{id: fmt.Sprintf("v%d", f.next), etag: fmt.Sprintf(`"etag-%d"`, f.next), body: body}
	f.objects[key] = append(versions, v)
	return &s3.PutObjectOutput{ETag: aws.String(v.etag), VersionId: aws.String(v.id)}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, aws.ToString(in.Prefix)) && k > aws.ToString(in.ContinuationToken) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := &s3.ListObjectsV2Output{}
	if int32(len(keys)) > f.pageSize {
		keys = keys[:f.pageSize]
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String(keys[len(keys)-1])
	}
	for _, k := range keys {
		out.Contents = append(out.Contents, s3types.Object{Key: aws.String(k)})
	}
	return out, nil
}

func (f *fakeS3) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, obj := range in.Delete.Objects {
		delete(f.objects, aws.ToString(obj.Key))
	}
	return &s3.DeleteObjectsOutput{}, nil
}

var now = time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)

func newTestStore() (*Store, *fakeS3) {
	f := newFakeS3()
	s := NewStore(f, "leopardai-reviewapp-state")
	s.now = func() time.Time { return now }
	return s, f
}

func TestNewSetsWallClockTTL(t *testing.T) {
	st := New(42, "abc123", "octocat", "https://pr-42.reviewapps.coventry.ai", now)

	if st.ID != "pr-42" || st.Status != StatusCreating {
		t.Fatalf("id, status = %q, %q", st.ID, st.Status)
	}
	if want := now.Add(48 * time.Hour); !st.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", st.ExpiresAt, want)
	}
	if st.Expired(now.Add(47*time.Hour)) || !st.Expired(now.Add(48*time.Hour)) {
		t.Fatal("expiry does not follow the 48h TTL")
	}
}

func TestCreateGetUpdate(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore()

	st := New(7, "abc", "octocat", "https://pr-7.reviewapps.coventry.ai", now)
	etag, err := s.Create(ctx, st)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, gotETag, err := s.Get(ctx, 7)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotETag != etag || got.URL != st.URL {
		t.Fatalf("get = %+v, %q", got, gotETag)
	}

	got.Status = StatusReady
	if _, err := s.Update(ctx, got, gotETag); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ = s.Get(ctx, 7)
	if got.Status != StatusReady {
		t.Fatalf("status = %q, want ready", got.Status)
	}
}

func TestGetMissing(t *testing.T) {
	s, _ := newTestStore()
	if _, _, err := s.Get(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateTwiceFails(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore()
	st := New(7, "abc", "octocat", "", now)

	if _, err := s.Create(ctx, st); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Create(ctx, st); !errors.Is(err, ErrExists) {
		t.Fatalf("second create err = %v, want ErrExists", err)
	}
}

func TestConcurrentUpdateLoses(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore()
	if _, err := s.Create(ctx, New(7, "abc", "octocat", "", now)); err != nil {
		t.Fatal(err)
	}

	// Two controller runs read the same version.
	a, etag, _ := s.Get(ctx, 7)
	b, _, _ := s.Get(ctx, 7)

	a.Status = StatusReady
	if _, err := s.Update(ctx, a, etag); err != nil {
		t.Fatalf("first update: %v", err)
	}
	b.Status = StatusFailed
	if _, err := s.Update(ctx, b, etag); !errors.Is(err, ErrConflict) {
		t.Fatalf("second update err = %v, want ErrConflict", err)
	}

	got, _, _ := s.Get(ctx, 7)
	if got.Status != StatusReady {
		t.Fatalf("status = %q, the losing write overwrote the state", got.Status)
	}
}

func TestListAndLimit(t *testing.T) {
	ctx := context.Background()
	s, f := newTestStore()
	f.pageSize = 3 // force pagination

	for pr := 1; pr <= DefaultLimit; pr++ {
		st := New(pr, "abc", "octocat", "", now)
		if pr == 3 {
			st.Status = StatusDestroying
		}
		if _, err := s.Create(ctx, st); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutManifest(ctx, &Manifest{PR: pr}); err != nil {
			t.Fatal(err)
		}
	}

	states, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(states) != DefaultLimit {
		t.Fatalf("listed %d review apps, want %d", len(states), DefaultLimit)
	}
	for i, st := range states {
		if st.PR != i+1 {
			t.Fatalf("states[%d].PR = %d, want ordered by PR", i, st.PR)
		}
	}
	if n := CountActive(states); n != DefaultLimit-1 {
		t.Fatalf("active = %d, want %d (destroying does not count)", n, DefaultLimit-1)
	}

	// Nine active: one more fits.
	if err := s.CheckLimit(ctx, DefaultLimit); err != nil {
		t.Fatalf("check limit with 9 active: %v", err)
	}
	if _, err := s.Create(ctx, New(11, "abc", "octocat", "", now)); err != nil {
		t.Fatal(err)
	}
	err = s.CheckLimit(ctx, DefaultLimit)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("check limit with 10 active err = %v, want ErrLimit", err)
	}
	if !strings.Contains(err.Error(), "10 of 10") {
		t.Fatalf("limit message = %q, want the count", err)
	}
}

func TestManifestReadsExactVersion(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore()

	first, err := s.PutManifest(ctx, &Manifest{PR: 7, SHA: "aaa", Images: map[string]string{"shell": "aaa"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutManifest(ctx, &Manifest{PR: 7, SHA: "bbb", Images: map[string]string{"shell": "bbb"}}); err != nil {
		t.Fatal(err)
	}

	m, err := s.GetManifest(ctx, 7, first)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	if m.SHA != "aaa" || m.Images["shell"] != "aaa" {
		t.Fatalf("manifest = %+v, want the first upload", m)
	}

	for name, ref := range map[string]ManifestRef{
		"wrong sha256":  {VersionID: first.VersionID, SHA256: "0000"},
		"no sha256":     {VersionID: first.VersionID},
		"no version id": {SHA256: first.SHA256},
	} {
		if _, err := s.GetManifest(ctx, 7, ref); !errors.Is(err, ErrManifestMismatch) {
			t.Fatalf("%s: err = %v, want ErrManifestMismatch", name, err)
		}
	}
}

func TestArchiveRemovesPrefix(t *testing.T) {
	ctx := context.Background()
	s, f := newTestStore()

	for _, pr := range []int{1, 10} {
		if _, err := s.Create(ctx, New(pr, "abc", "octocat", "", now)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutManifest(ctx, &Manifest{PR: pr}); err != nil {
			t.Fatal(err)
		}
	}

	key, err := s.Archive(ctx, 1)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if key != "archive/pr-1/20261029T150000Z.json" {
		t.Fatalf("archive key = %q", key)
	}
	if _, ok := f.objects[key]; !ok {
		t.Fatal("final state was not archived")
	}
	for k := range f.objects {
		if strings.HasPrefix(k, "reviewapps/pr-1/") {
			t.Fatalf("%s left behind after archive", k)
		}
	}
	// pr-10 shares the "pr-1" prefix and must survive.
	if _, _, err := s.Get(ctx, 10); err != nil {
		t.Fatalf("pr-10 was touched: %v", err)
	}
}

func TestRecordKeepsRecentHistory(t *testing.T) {
	st := New(7, "abc", "octocat", "", now)
	for i := range historyLimit + 5 {
		st.Record(HistoryEntry{Action: fmt.Sprintf("deploy-%d", i), At: now})
	}
	if len(st.History) != historyLimit {
		t.Fatalf("history has %d entries, want %d", len(st.History), historyLimit)
	}
	if st.History[0].Action != "deploy-5" {
		t.Fatalf("oldest entry = %q, want deploy-5", st.History[0].Action)
	}
}

func TestPRFromStateKey(t *testing.T) {
	cases := map[string]int{
		"reviewapps/pr-7/state.json":    7,
		"reviewapps/pr-123/state.json":  123,
		"reviewapps/pr-7/manifest.json": 0,
		"reviewapps/pr-x/state.json":    0,
		"reviewapps/pr-07/state.json":   0,
		"archive/pr-7/state.json":       0,
	}
	for key, want := range cases {
		got, ok := prFromStateKey(key)
		if (want != 0) != ok || got != want {
			t.Errorf("prFromStateKey(%q) = %d, %v; want %d", key, got, ok, want)
		}
	}
}
