import { existsSync } from "node:fs";

const packages = ["clients/tui", "clients/web", "clients/shared-ts"];

for (const pkg of packages) {
  if (!existsSync(`${pkg}/src/index.ts`)) {
    throw new Error(`missing ${pkg}/src/index.ts`);
  }
}

console.log("typecheck stub passed");
