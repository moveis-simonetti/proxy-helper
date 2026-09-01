package serve

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

// timeoutErr mimics net/http's private "timeout awaiting response headers"
// error: a net.Error whose Timeout() is true, produced AFTER the dial
// succeeded. The real type is unexported, so the classification must work
// off the interface, and this stand-in proves it does.
type timeoutErr struct{ msg string }

func (e timeoutErr) Error() string   { return e.msg }
func (e timeoutErr) Timeout() bool   { return true }
func (e timeoutErr) Temporary() bool { return true }

func TestFailMessageClassifiesErrors(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	headerTimeout := timeoutErr{msg: "net/http: timeout awaiting response headers"}

	for _, tc := range []struct {
		name    string
		err     error
		want    string
		wantNot string
	}{
		// A dial failure really is "could not reach".
		{"dial timeout", dialErr, "could not reach 192.168.111.70:3128", ""},
		// The bug this guards against: a response-header timeout used to be
		// reported as "could not reach", but the upstream WAS reached —
		// the origin just never started answering.
		{"header timeout", headerTimeout, "did not answer in time", "could not reach"},
		{"other error", errors.New("upstream proxy x answered CONNECT with 403"), "request to 192.168.111.70:3128 failed", "could not reach"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := failMessage("192.168.111.70:3128", tc.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("failMessage = %q, esperava conter %q", got, tc.want)
			}
			if tc.wantNot != "" && strings.Contains(got, tc.wantNot) {
				t.Errorf("failMessage = %q, não deveria conter %q", got, tc.wantNot)
			}
		})
	}
}

// A dial error that is ALSO a timeout must still read as unreachable: the
// dial case has to win over the generic timeout case.
func TestFailMessageDialTimeoutIsStillUnreachable(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	if !err.Timeout() {
		t.Fatal("premissa quebrada: o erro de dial do teste não é um timeout")
	}
	got := failMessage("10.0.0.1:3128", err)
	if !strings.Contains(got, "could not reach") {
		t.Errorf("failMessage = %q, esperava \"could not reach\"", got)
	}
}
