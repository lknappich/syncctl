package versionparity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRow struct {
	val string
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.val
	return nil
}

type fakeQ struct{ row fakeRow }

func (q fakeQ) QueryRow(context.Context, string, ...any) Row { return q.row }

func pg(v string) Querier { return fakeQ{fakeRow{val: v}} }

type fakeRunner struct {
	out map[string]string
	err map[string]error
}

func (f fakeRunner) Output(_ context.Context, host, _ string) ([]byte, error) {
	if e := f.err[host]; e != nil {
		return nil, e
	}
	return []byte(f.out[host]), nil
}

func TestComparePostgres(t *testing.T) {
	boom := fakeQ{fakeRow{err: errors.New("boom")}}
	tests := []struct {
		name string
		p, s Querier
		want Status
		sub  string
	}{
		{"match", pg("160004"), pg("160011"), StatusOK, "primary 16, secondary 16"},
		{"mismatch", pg("160004"), pg("150005"), StatusFail, "primary 16, secondary 15"},
		{"primary query error", boom, pg("160004"), StatusFail, "primary: query"},
		{"secondary query error", pg("160004"), boom, StatusFail, "secondary: query"},
		{"unparsable", pg("abc"), pg("160004"), StatusFail, "unparsable"},
		{"too small", pg("42"), pg("160004"), StatusFail, "< 10 unsupported"},
		{"pg9", pg("90624"), pg("160004"), StatusFail, "< 10 unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ComparePostgres(context.Background(), tt.p, tt.s)
			if r.Status != tt.want || !strings.Contains(r.Message, tt.sub) {
				t.Fatalf("got %s %q, want %s containing %q", r.Status, r.Message, tt.want, tt.sub)
			}
		})
	}
}

func TestParseGitLabVersion(t *testing.T) {
	good := map[string]string{
		"gitlab-ce 16.5.1\nfoo 1.2.3": "16.5.1-ce",
		"gitlab-ee 16.5.1 abc\n":      "16.5.1-ee",
		"16.5.1":                      "16.5.1",
		"v16.5.1-ce":                  "16.5.1-ce",
		"  gitlab-ce 17.0.0+ce.0\n":   "17.0.0-ce",
	}
	for in, want := range good {
		v, err := ParseGitLabVersion(in)
		if err != nil || v.String() != want {
			t.Errorf("%q: got %v, %v; want %s", in, v, err, want)
		}
	}
	for _, in := range []string{"", "garbage", "gitlab-ce 16.5", "No such file"} {
		if _, err := ParseGitLabVersion(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestCompareGitLab(t *testing.T) {
	run := func(p, s string) fakeRunner {
		return fakeRunner{out: map[string]string{"p.example": p, "s.example": s}}
	}
	tests := []struct {
		name   string
		runner fakeRunner
		p, s   string
		want   Status
		sub    string
	}{
		{"same", run("gitlab-ce 16.5.1\n", "gitlab-ce 16.5.1\n"), "p.example", "s.example", StatusOK, "matches"},
		{"patch differs", run("gitlab-ce 16.5.1", "gitlab-ce 16.5.3"), "p.example", "s.example", StatusWarn, "16.5.3-ce"},
		{"minor differs", run("gitlab-ce 16.5.1", "gitlab-ce 16.6.1"), "p.example", "s.example", StatusFail, "mismatch"},
		{"major differs", run("gitlab-ce 16.5.1", "gitlab-ce 17.5.1"), "p.example", "s.example", StatusFail, "mismatch"},
		{"unparsable primary", run("oops", "gitlab-ce 16.5.1"), "p.example", "s.example", StatusFail, "primary: unparsable"},
		{"unparsable secondary", run("gitlab-ce 16.5.1", ""), "p.example", "s.example", StatusFail, "secondary: unparsable"},
		{"ssh error", fakeRunner{err: map[string]error{"s.example": errors.New("refused")}, out: map[string]string{"p.example": "16.5.1"}}, "p.example", "s.example", StatusFail, "secondary: ssh s.example: refused"},
		{"ssh warning line", run("Warning: Permanently added 'p' (ED25519) to the list of known hosts.\ngitlab-ce 16.5.1\n", "gitlab-ce 16.5.1"), "p.example", "s.example", StatusOK, "matches"},
		{"edition mismatch", run("gitlab-ce 16.5.1", "gitlab-ee 16.5.1"), "p.example", "s.example", StatusFail, "edition mismatch"},
		{"edition unknown on one side", run("gitlab-ce 16.5.1", "16.5.1"), "p.example", "s.example", StatusWarn, "edition"},
		{"bad host", run("16.5.1", "16.5.1"), "-oProxyCommand=x", "s.example", StatusFail, "primary:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := CompareGitLab(context.Background(), tt.p, tt.s, tt.runner)
			if r.Status != tt.want || !strings.Contains(r.Message, tt.sub) {
				t.Fatalf("got %s %q, want %s containing %q", r.Status, r.Message, tt.want, tt.sub)
			}
		})
	}
}

func TestRunNilDeps(t *testing.T) {
	rep := Run(context.Background(), Sites{})
	if len(rep.Results) != 2 || rep.Worst() != StatusFail {
		t.Fatalf("unexpected: %+v", rep)
	}
}

func TestRunAndWorst(t *testing.T) {
	runner := fakeRunner{out: map[string]string{"p.example": "gitlab-ce 16.5.1", "s.example": "gitlab-ce 16.5.2"}}
	rep := Run(context.Background(), Sites{
		PrimarySSH: "p.example", SecondarySSH: "s.example",
		PrimaryDB: pg("160004"), SecondaryDB: pg("160004"), Runner: runner,
	})
	if len(rep.Results) != 2 || rep.Worst() != StatusWarn {
		t.Fatalf("unexpected report: %+v worst=%s", rep.Results, rep.Worst())
	}
	rep.Results = append(rep.Results, Result{Status: StatusFail})
	if rep.Worst() != StatusFail || (Report{}).Worst() != StatusOK {
		t.Fatal("Worst wrong")
	}
}

func (f fakeRunner) CombinedOutput(ctx context.Context, host, cmd string) ([]byte, error) {
	return f.Output(ctx, host, cmd)
}
