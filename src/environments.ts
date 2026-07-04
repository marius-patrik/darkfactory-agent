import type { SharedState } from "./state";

export type PackageTargetKind = "host" | "container";
export type EnvironmentKind = "host" | "container" | "agent-workspace";

export interface DistroPackageRecord {
  id: string;
  target: PackageTargetKind;
  manager: "apt" | "apk" | "pacman" | "winget" | "brew" | "custom";
  name: string;
  version?: string;
  source?: string;
}

export interface ContainerPackageRecord {
  id: string;
  image: string;
  digest?: string;
  tags?: string[];
  runtime?: "docker" | "podman";
}

export interface EnvironmentRecord {
  id: string;
  kind: EnvironmentKind;
  packages: string[];
  secretsScope?: string;
  containerPackage?: string;
  workspacePath?: string;
  createdAt: string;
}

export interface PackagesAndEnvironmentsState {
  schemaVersion: 1;
  activeEnvironmentId?: string;
  distroPackages: DistroPackageRecord[];
  containerPackages: ContainerPackageRecord[];
  environments: EnvironmentRecord[];
}

export function emptyPackagesAndEnvironmentsState(): PackagesAndEnvironmentsState {
  return {
    schemaVersion: 1,
    distroPackages: [],
    containerPackages: [],
    environments: [],
  };
}

export async function readPackagesAndEnvironmentsState(state: SharedState): Promise<PackagesAndEnvironmentsState> {
  if (!(await Bun.file(state.environmentsFile).exists())) return emptyPackagesAndEnvironmentsState();
  const raw = JSON.parse(await Bun.file(state.environmentsFile).text()) as Partial<PackagesAndEnvironmentsState>;
  return {
    schemaVersion: 1,
    activeEnvironmentId: raw.activeEnvironmentId,
    distroPackages: raw.distroPackages ?? [],
    containerPackages: raw.containerPackages ?? [],
    environments: raw.environments ?? [],
  };
}

export function notImplementedPackagesAndEnvironments(feature: string): Error {
  return new Error(
    `${feature} is not yet implemented; real OS/container package and environment plumbing depends on agents-mono#8 and agents-mono#9`,
  );
}
