package sysproc

import (
	"os"
	"testing"
)

func TestStartDetachedStartsAProgramAndReturns(t *testing.T) {
	if err := StartDetached(os.Args[0], "-test.run=^$"); err != nil {
		t.Fatal(err)
	}
	if err := StartDetached("no-such-program-for-the-test"); err == nil {
		t.Fatal("a missing program must be an error")
	}
}
