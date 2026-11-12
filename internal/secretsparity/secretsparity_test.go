package secretsparity

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	out map[string]string
	err map[string]error
}

func (f fakeRunner) Output(_ context.Context, host, cmd string) ([]byte, error) {
	if cmd != SecretsCmd {
		return nil, fmt.Errorf("unexpected cmd %q", cmd)
	}
	return []byte(f.out[host]), f.err[host]
}

// CombinedOutput mimics ssh: stderr noise is merged into the output. The
// package must never use it for reading the secrets file.
func (f fakeRunner) CombinedOutput(_ context.Context, host, cmd string) ([]byte, error) {
	return []byte("Warning: Permanently added host\n" + f.out[host]), f.err[host]
}

func run(t *testing.T, a, b string) (Result, error) {
	t.Helper()
	return Compare(context.Background(), fakeRunner{out: map[string]string{"a.example": a, "b.example": b}}, "a.example", "b.example")
}

func TestEqual(t *testing.T) {
	j := `{"gitlab_rails":{"db_key_base":"S3CRET-one","otp_key_base":"S3CRET-two"},"n":1,"t":true,"z":null}`
	r, err := run(t, j, j)
	if err != nil || !r.Equal() {
		t.Fatalf("want equal, got %+v %v", r, err)
	}
}

func TestDifferingKey(t *testing.T) {
	r, err := run(t,
		`{"gitlab_rails":{"db_key_base":"aaa","otp_key_base":"same"}}`,
		`{"gitlab_rails":{"db_key_base":"bbb","otp_key_base":"same"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Differ, []string{"gitlab_rails.db_key_base"}) || len(r.OnlyA)+len(r.OnlyB) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestMissingKeys(t *testing.T) {
	r, err := run(t, `{"x":"1","onlya":"v"}`, `{"x":"1","onlyb":"v"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.OnlyA, []string{"onlya"}) || !reflect.DeepEqual(r.OnlyB, []string{"onlyb"}) || len(r.Differ) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestNestedAndArrays(t *testing.T) {
	r, err := run(t,
		`{"a":{"b":{"c":"1"}},"l":["x","y",{"k":"v"}],"e":[],"o":{},"n":1}`,
		`{"a":{"b":{"c":"2"}},"l":["x","z",{"k":"v"},"extra"],"e":[],"o":{},"n":"1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Differ, []string{"a.b.c", "l[1]", "n"}) {
		t.Fatalf("differ %v", r.Differ)
	}
	if !reflect.DeepEqual(r.OnlyB, []string{"l[3]"}) || len(r.OnlyA) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestInvalidJSON(t *testing.T) {
	for _, bad := range []string{`not json`, `{"a":`, `[1,2]`, `{"a":"b"} trailing`, ``} {
		if _, err := run(t, bad, `{}`); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestNoSecretLeaks(t *testing.T) {
	const sa, sb = "TOPSECRET-AAA-111", "TOPSECRET-BBB-222"
	r, err := run(t,
		`{"k":{"v":"`+sa+`"},"onlya":"`+sa+`x"}`,
		`{"k":{"v":"`+sb+`"},"onlyb":"`+sb+`x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if all := fmt.Sprintf("%v %+v %#v", r, r, r); strings.Contains(all, "TOPSECRET") {
		t.Fatalf("value leaked: %s", all)
	}
	// Invalid JSON containing a secret must not leak through the error.
	_, err = run(t, `{"k":"`+sa+`" garbage`, sb)
	if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("err=%v", err)
	}
	// Runner output accompanying an error must not leak either.
	f := fakeRunner{
		out: map[string]string{"a.example": sa},
		err: map[string]error{"a.example": errors.New("exit status 1")},
	}
	_, err = Compare(context.Background(), f, "a.example", "b.example")
	if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("err=%v", err)
	}
}

func TestStderrNoiseIgnored(t *testing.T) {
	j := `{"a":"1"}`
	r, err := run(t, j, j) // CombinedOutput of the fake would be corrupted
	if err != nil || !r.Equal() {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestPathCollision(t *testing.T) {
	_, err := run(t, `{"a.b":"1","a":{"b":"2"}}`, `{}`)
	if err == nil || strings.Contains(err.Error(), "1") && strings.Contains(err.Error(), "2") {
		t.Fatalf("err=%v", err)
	}
	if err == nil {
		t.Fatal("expected collision error")
	}
}

func TestControlCharKeyRejected(t *testing.T) {
	_, err := run(t, "{\"a\\nb\":\"SECRETVAL\"}", `{}`)
	if err == nil || strings.Contains(err.Error(), "SECRETVAL") {
		t.Fatalf("err=%v", err)
	}
}

func TestNumericLiteralsDiffer(t *testing.T) {
	r, err := run(t, `{"n":1}`, `{"n":1.0}`)
	if err != nil || !reflect.DeepEqual(r.Differ, []string{"n"}) {
		t.Fatalf("got %+v %v", r, err)
	}
}
