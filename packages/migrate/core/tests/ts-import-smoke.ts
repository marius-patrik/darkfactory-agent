import { create } from "@bufbuild/protobuf";
// The generated contract surface moved to packages/sdk when the sdk took
// ownership of contracts, so this smoke reaches it by path rather than through
// a workspace link that no longer resolves from inside the carried core tree.
import { name as sharedName } from "../../../sdk/shared-ts/src/index";
import {
  ListModelsRequestSchema,
  RegistryService,
  file_agent_os_v1_common,
} from "../../../sdk/shared-ts/src/gen/index";

const request = create(ListModelsRequestSchema, {});

if (
  sharedName !== "@agent-os/shared-ts" ||
  request.$typeName !== "agent_os.v1.ListModelsRequest" ||
  file_agent_os_v1_common.name !== "agent_os/v1/common" ||
  RegistryService.typeName !== "agent_os.v1.RegistryService"
) {
  throw new Error("Andromeda core TypeScript import smoke test failed");
}
