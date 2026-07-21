import { create } from "@bufbuild/protobuf";
// The generated contract surface moved to src/sdk when the sdk took
// ownership of contracts, so this smoke reaches it by path rather than through
// a workspace link that no longer resolves from inside the carried core tree.
import { name as sharedName } from "../shared-ts/index";
import {
  ListModelsRequestSchema,
  RegistryService,
  file_andromeda_v1_common,
} from "../shared-ts/gen/index";

const request = create(ListModelsRequestSchema, {});

if (
  sharedName !== "@marius-patrik/andromeda-sdk" ||
  request.$typeName !== "andromeda.v1.ListModelsRequest" ||
  file_andromeda_v1_common.name !== "andromeda/v1/common" ||
  RegistryService.typeName !== "andromeda.v1.RegistryService"
) {
  throw new Error("Andromeda core TypeScript import smoke test failed");
}
