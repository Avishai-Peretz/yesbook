package postgres_test

import (
	"os"
	"testing"

	"github.com/avi-p/yesbook/internal/operations/readcontract"
)

func TestOperationsContract(t *testing.T) {
	if os.Getenv("YESBOOK_TEST_DATABASE_URL") == "" {
		t.Skip("YESBOOK_TEST_DATABASE_URL is not set")
	}
	readcontract.Run(t, func(t *testing.T) readcontract.Store { return newStore(t) })
}
