package disk

import "testing"

func TestOfReportsTheDiskOfAFolder(t *testing.T) {
	usage, err := Of(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total == 0 || usage.Free > usage.Total {
		t.Errorf("%+v", usage)
	}
}

func TestOfFailsForAMissingFolder(t *testing.T) {
	if _, err := Of(t.TempDir() + "/no/such/folder"); err == nil {
		t.Error("expected an error")
	}
}
