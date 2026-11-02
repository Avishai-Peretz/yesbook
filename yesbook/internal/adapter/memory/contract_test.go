package memory_test

import (
	"testing"

	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/adapter/storecontract"
)

func TestRepositoryContract(t *testing.T) {
	storecontract.Run(t, func(*testing.T) storecontract.Store { return memory.New() })
}
