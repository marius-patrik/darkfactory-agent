package contractsgo_test

import (
	"testing"

	andromedav1 "github.com/marius-patrik/andromeda/packages/sdk/contracts-go/gen/andromeda/v1"
	"github.com/marius-patrik/andromeda/packages/sdk/contracts-go/gen/andromeda/v1/andromedav1connect"
)

// TestGeneratedGoContractsAreImportable proves that downstream Go consumers can
// import the generated messages and Connect service definitions from the
// contracts-go module.
func TestGeneratedGoContractsAreImportable(t *testing.T) {
	req := &andromedav1.ListModelsRequest{}
	if got := req.ProtoReflect().Descriptor().FullName(); got != "andromeda.v1.ListModelsRequest" {
		t.Fatalf("unexpected message descriptor: %v", got)
	}

	if andromedav1connect.RegistryServiceName != "andromeda.v1.RegistryService" {
		t.Fatalf("unexpected service name: %s", andromedav1connect.RegistryServiceName)
	}

	frame := &andromedav1.ServerFrame{}
	if frame.ProtoReflect().Descriptor().FullName() != "andromeda.v1.ServerFrame" {
		t.Fatalf("unexpected frame descriptor: %v", frame.ProtoReflect().Descriptor().FullName())
	}
}
