package memory_test

import (
	"testing"

	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/operations/readcontract"
)

func TestOperationsContract(t *testing.T) {
	readcontract.Run(t, func(*testing.T) readcontract.Store { return memory.New() })
}
