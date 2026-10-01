package town

import (
	"context"
	"os"
	"testing"
)

// writeTestCommand installs a POSIX shell stub for a command Town shells out
// to, such as gh or git.
func writeTestCommand(t *testing.T, path, body string) {
	t.Helper()
	skipPOSIXFakes(t)
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

// botRunHook records the request Town dispatches and answers with a fixed
// result, standing in for the in-process bot.
func botRunHook(capture *workerRequest, result workerResult) func(context.Context, Role, workerRequest, bool, func(Progress)) (workerResult, error) {
	return func(_ context.Context, _ Role, request workerRequest, _ bool, _ func(Progress)) (workerResult, error) {
		if capture != nil {
			*capture = request
		}
		return result, nil
	}
}
