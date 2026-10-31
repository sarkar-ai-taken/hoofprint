package memory

import (
	"testing"

	"github.com/sarkar-ai-taken/hoofprint/internal/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, New())
}
