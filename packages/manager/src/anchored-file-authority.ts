import { createHash } from "node:crypto";
import {
  closeSync,
  constants as fsConstants,
  fstatSync,
  fsyncSync,
  ftruncateSync,
  openSync,
  readFileSync,
  writeSync,
} from "node:fs";
import { realpath } from "node:fs/promises";
import path from "node:path";
import type { Pointer } from "bun:ffi";

/**
 * The manager's one mutation boundary for receipt and provider-requested file
 * writes. It is deliberately not a general filesystem helper.
 *
 * Invariants:
 * - callers supply a canonical physical root and component-only relative path;
 * - every ancestor is opened without following links and held through mutation;
 * - a second root-anchored traversal must reproduce every held identity before
 *   the first create/truncate/write side effect;
 * - replace/verify operations require one regular, singly-linked target;
 * - Windows handles deny delete sharing, so admitted ancestors and the target
 *   cannot be renamed or replaced while the operation is live;
 * - receipt updates additionally bind both identities and the prior content;
 * - platforms without one of the audited implementations fail closed.
 */

const MAX_AUTHORITY_CONTENT_BYTES = 16 * 1024 * 1024;
const WINDOWS_RESERVED_COMPONENT = /^(?:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?$/i;

export type AnchoredFileOperation = "create" | "replace" | "verify";

export interface AnchoredFileIdentity {
  volume: string;
  file: string;
}

export interface AnchoredFileProof {
  root: AnchoredFileIdentity;
  file: AnchoredFileIdentity;
  contentSha256: string;
}

export interface AnchoredFileAuthorityLifecycle {
  /** Deterministic regression seam; production callers never provide it. */
  beforeFinalTraversal?: () => Promise<void>;
}

export interface AnchoredFileAuthorityRequest {
  operation: AnchoredFileOperation;
  root: string;
  components: readonly string[];
  content?: Uint8Array;
  expected?: AnchoredFileProof;
  lifecycle?: AnchoredFileAuthorityLifecycle;
}

export class AnchoredFileAuthorityError extends Error {
  constructor(
    readonly reason:
      | "unsupported_platform"
      | "invalid_request"
      | "containment_changed"
      | "target_unavailable"
      | "identity_changed"
      | "content_changed"
      | "mutation_failed",
  ) {
    super(`anchored file authority denied: ${reason}`);
  }
}

function samePath(left: string, right: string): boolean {
  const normalizedLeft = path.resolve(left);
  const normalizedRight = path.resolve(right);
  return process.platform === "win32"
    ? normalizedLeft.toLowerCase() === normalizedRight.toLowerCase()
    : normalizedLeft === normalizedRight;
}

function sameIdentity(left: AnchoredFileIdentity, right: AnchoredFileIdentity): boolean {
  return left.volume === right.volume && left.file === right.file;
}

function sameProof(left: AnchoredFileProof, right: AnchoredFileProof): boolean {
  return (
    sameIdentity(left.root, right.root) &&
    sameIdentity(left.file, right.file) &&
    left.contentSha256 === right.contentSha256
  );
}

function digest(content: Uint8Array): string {
  return createHash("sha256").update(content).digest("hex");
}

function validateComponents(components: readonly string[]): void {
  if (components.length === 0) throw new AnchoredFileAuthorityError("invalid_request");
  for (const component of components) {
    if (
      !component ||
      component === "." ||
      component === ".." ||
      component.includes("\0") ||
      component.includes(path.sep)
    ) {
      throw new AnchoredFileAuthorityError("invalid_request");
    }
    if (
      process.platform === "win32" &&
      (/[<>:"/\\|?*]/.test(component) ||
        component.endsWith(".") ||
        component.endsWith(" ") ||
        WINDOWS_RESERVED_COMPONENT.test(component))
    ) {
      throw new AnchoredFileAuthorityError("invalid_request");
    }
  }
}

function normalizedContent(request: AnchoredFileAuthorityRequest): Buffer {
  if (request.operation === "verify") {
    if (request.content !== undefined) throw new AnchoredFileAuthorityError("invalid_request");
    return Buffer.alloc(0);
  }
  if (!request.content) throw new AnchoredFileAuthorityError("invalid_request");
  const content = Buffer.from(request.content);
  if (content.length > MAX_AUTHORITY_CONTENT_BYTES) {
    throw new AnchoredFileAuthorityError("invalid_request");
  }
  return content;
}

function validateRequest(request: AnchoredFileAuthorityRequest): Buffer {
  if (!path.isAbsolute(request.root) || request.root.includes("\0")) {
    throw new AnchoredFileAuthorityError("invalid_request");
  }
  validateComponents(request.components);
  if (request.operation === "create" && request.expected) {
    throw new AnchoredFileAuthorityError("invalid_request");
  }
  if (request.operation === "verify" && !request.expected) {
    throw new AnchoredFileAuthorityError("invalid_request");
  }
  return normalizedContent(request);
}

function posixIdentity(info: ReturnType<typeof fstatSync>): AnchoredFileIdentity {
  return { volume: String(info.dev), file: String(info.ino) };
}

function closeFileDescriptors(descriptors: readonly number[]): void {
  for (const descriptor of [...descriptors].reverse()) {
    try {
      closeSync(descriptor);
    } catch {
      // The primary authority result must not be replaced by cleanup noise.
    }
  }
}

async function loadPosixRuntime() {
  try {
    const ffi = await import("bun:ffi");
    const library = process.platform === "linux"
      ? "libc.so.6"
      : process.platform === "darwin"
        ? "/usr/lib/libSystem.B.dylib"
        : null;
    if (!library) throw new AnchoredFileAuthorityError("unsupported_platform");
    const native = ffi.dlopen(library, {
      openat: {
        args: ["i32", "ptr", "i32", "u32"],
        returns: "i32",
      },
    } as const);
    return { openat: native.symbols.openat, ptr: ffi.ptr };
  } catch (error) {
    if (error instanceof AnchoredFileAuthorityError) throw error;
    throw new AnchoredFileAuthorityError("unsupported_platform");
  }
}

let posixRuntimePromise: ReturnType<typeof loadPosixRuntime> | null = null;

function posixRuntime(): ReturnType<typeof loadPosixRuntime> {
  posixRuntimePromise ??= loadPosixRuntime();
  return posixRuntimePromise;
}

type PosixRuntime = Awaited<ReturnType<typeof loadPosixRuntime>>;

interface PosixChain {
  descriptors: number[];
  identities: AnchoredFileIdentity[];
}

function openAt(
  runtime: PosixRuntime,
  parent: number,
  component: string,
  flags: number,
  mode = 0,
): number {
  const name = Buffer.from(`${component}\0`, "utf8");
  const descriptor = runtime.openat(parent, runtime.ptr(name), flags, mode);
  if (!Number.isSafeInteger(descriptor) || descriptor < 0) {
    throw new AnchoredFileAuthorityError("target_unavailable");
  }
  return descriptor;
}

function posixDirectoryFlags(): number {
  const noFollow = fsConstants.O_NOFOLLOW ?? 0;
  const directory = fsConstants.O_DIRECTORY ?? 0;
  const closeOnExec = (fsConstants as Record<string, number>).O_CLOEXEC ?? 0;
  if (!noFollow || !directory) throw new AnchoredFileAuthorityError("unsupported_platform");
  return fsConstants.O_RDONLY | noFollow | directory | closeOnExec;
}

function openPosixChain(
  runtime: PosixRuntime,
  root: string,
  directories: readonly string[],
): PosixChain {
  const descriptors: number[] = [];
  const identities: AnchoredFileIdentity[] = [];
  try {
    const rootDescriptor = openSync(root, posixDirectoryFlags());
    descriptors.push(rootDescriptor);
    const rootInfo = fstatSync(rootDescriptor, { bigint: true });
    if (!rootInfo.isDirectory()) throw new AnchoredFileAuthorityError("containment_changed");
    identities.push(posixIdentity(rootInfo));
    for (const component of directories) {
      const descriptor = openAt(runtime, descriptors.at(-1)!, component, posixDirectoryFlags());
      descriptors.push(descriptor);
      const info = fstatSync(descriptor, { bigint: true });
      if (!info.isDirectory()) throw new AnchoredFileAuthorityError("containment_changed");
      identities.push(posixIdentity(info));
    }
    return { descriptors, identities };
  } catch (error) {
    closeFileDescriptors(descriptors);
    if (error instanceof AnchoredFileAuthorityError) throw error;
    throw new AnchoredFileAuthorityError("containment_changed");
  }
}

function sameChain(left: PosixChain, right: PosixChain): boolean {
  return (
    left.identities.length === right.identities.length &&
    left.identities.every((identity, index) => sameIdentity(identity, right.identities[index]!))
  );
}

function posixTargetFlags(write: boolean): number {
  const noFollow = fsConstants.O_NOFOLLOW ?? 0;
  const closeOnExec = (fsConstants as Record<string, number>).O_CLOEXEC ?? 0;
  if (!noFollow) throw new AnchoredFileAuthorityError("unsupported_platform");
  return (write ? fsConstants.O_RDWR : fsConstants.O_RDONLY) | noFollow | closeOnExec;
}

function checkedPosixTarget(descriptor: number): {
  identity: AnchoredFileIdentity;
  contentSize: bigint;
} {
  const info = fstatSync(descriptor, { bigint: true });
  if (!info.isFile() || info.nlink !== 1n) {
    throw new AnchoredFileAuthorityError("containment_changed");
  }
  return { identity: posixIdentity(info), contentSize: info.size };
}

function writePosixContent(descriptor: number, content: Buffer): void {
  ftruncateSync(descriptor, 0);
  let offset = 0;
  while (offset < content.length) {
    const written = writeSync(descriptor, content, offset, content.length - offset, offset);
    if (!Number.isSafeInteger(written) || written <= 0) {
      throw new AnchoredFileAuthorityError("mutation_failed");
    }
    offset += written;
  }
  fsyncSync(descriptor);
}

async function runPosixAuthority(
  request: AnchoredFileAuthorityRequest,
  canonicalRoot: string,
  content: Buffer,
): Promise<AnchoredFileProof> {
  const runtime = await posixRuntime();
  const directories = request.components.slice(0, -1);
  const targetName = request.components.at(-1)!;
  const heldDescriptors: number[] = [];
  try {
    const first = openPosixChain(runtime, canonicalRoot, directories);
    heldDescriptors.push(...first.descriptors);
    let firstTargetDescriptor: number | null = null;
    let firstTargetIdentity: AnchoredFileIdentity | null = null;
    if (request.operation !== "create") {
      firstTargetDescriptor = openAt(
        runtime,
        first.descriptors.at(-1)!,
        targetName,
        posixTargetFlags(false),
      );
      heldDescriptors.push(firstTargetDescriptor);
      firstTargetIdentity = checkedPosixTarget(firstTargetDescriptor).identity;
      if (request.expected) {
        if (
          !sameIdentity(first.identities[0]!, request.expected.root) ||
          !sameIdentity(firstTargetIdentity, request.expected.file)
        ) {
          throw new AnchoredFileAuthorityError("identity_changed");
        }
      }
    }

    await request.lifecycle?.beforeFinalTraversal?.();

    const finalChain = openPosixChain(runtime, canonicalRoot, directories);
    heldDescriptors.push(...finalChain.descriptors);
    if (!sameChain(first, finalChain)) {
      throw new AnchoredFileAuthorityError("containment_changed");
    }

    const createFlags =
      posixTargetFlags(true) | fsConstants.O_CREAT | fsConstants.O_EXCL;
    const finalTargetDescriptor = openAt(
      runtime,
      finalChain.descriptors.at(-1)!,
      targetName,
      request.operation === "create" ? createFlags : posixTargetFlags(request.operation === "replace"),
      0o600,
    );
    heldDescriptors.push(finalTargetDescriptor);
    const finalTarget = checkedPosixTarget(finalTargetDescriptor);
    if (firstTargetIdentity && !sameIdentity(firstTargetIdentity, finalTarget.identity)) {
      throw new AnchoredFileAuthorityError("identity_changed");
    }
    if (
      request.expected &&
      (!sameIdentity(finalChain.identities[0]!, request.expected.root) ||
        !sameIdentity(finalTarget.identity, request.expected.file))
    ) {
      throw new AnchoredFileAuthorityError("identity_changed");
    }

    let contentSha256: string;
    if (request.operation === "verify") {
      if (finalTarget.contentSize > BigInt(MAX_AUTHORITY_CONTENT_BYTES)) {
        throw new AnchoredFileAuthorityError("content_changed");
      }
      const existing = readFileSync(finalTargetDescriptor);
      contentSha256 = digest(existing);
      if (!sameProof(
        {
          root: finalChain.identities[0]!,
          file: finalTarget.identity,
          contentSha256,
        },
        request.expected!,
      )) {
        throw new AnchoredFileAuthorityError("content_changed");
      }
    } else {
      if (request.expected) {
        if (finalTarget.contentSize > BigInt(MAX_AUTHORITY_CONTENT_BYTES)) {
          throw new AnchoredFileAuthorityError("content_changed");
        }
        const existing = readFileSync(finalTargetDescriptor);
        if (digest(existing) !== request.expected.contentSha256) {
          throw new AnchoredFileAuthorityError("content_changed");
        }
      }
      writePosixContent(finalTargetDescriptor, content);
      const after = checkedPosixTarget(finalTargetDescriptor);
      if (!sameIdentity(after.identity, finalTarget.identity)) {
        throw new AnchoredFileAuthorityError("identity_changed");
      }
      contentSha256 = digest(content);
    }

    return {
      root: finalChain.identities[0]!,
      file: finalTarget.identity,
      contentSha256,
    };
  } catch (error) {
    if (error instanceof AnchoredFileAuthorityError) throw error;
    throw new AnchoredFileAuthorityError("mutation_failed");
  } finally {
    closeFileDescriptors(heldDescriptors);
  }
}

async function loadWindowsRuntime() {
  try {
    const ffi = await import("bun:ffi");
    const native = ffi.dlopen("kernel32.dll", {
      CreateFileW: {
        args: ["ptr", "u32", "u32", "ptr", "u32", "u32", "ptr"],
        returns: "ptr",
      },
      GetFileInformationByHandle: {
        args: ["ptr", "ptr"],
        returns: "i32",
      },
      SetFilePointerEx: {
        args: ["ptr", "i64", "ptr", "u32"],
        returns: "i32",
      },
      SetEndOfFile: {
        args: ["ptr"],
        returns: "i32",
      },
      ReadFile: {
        args: ["ptr", "ptr", "u32", "ptr", "ptr"],
        returns: "i32",
      },
      WriteFile: {
        args: ["ptr", "ptr", "u32", "ptr", "ptr"],
        returns: "i32",
      },
      FlushFileBuffers: {
        args: ["ptr"],
        returns: "i32",
      },
      CloseHandle: {
        args: ["ptr"],
        returns: "i32",
      },
    } as const);
    return { symbols: native.symbols, ptr: ffi.ptr };
  } catch (error) {
    if (error instanceof AnchoredFileAuthorityError) throw error;
    throw new AnchoredFileAuthorityError("unsupported_platform");
  }
}

let windowsRuntimePromise: ReturnType<typeof loadWindowsRuntime> | null = null;

function windowsRuntime(): ReturnType<typeof loadWindowsRuntime> {
  windowsRuntimePromise ??= loadWindowsRuntime();
  return windowsRuntimePromise;
}

type WindowsRuntime = Awaited<ReturnType<typeof loadWindowsRuntime>>;
type WindowsHandle = Pointer;

const GENERIC_READ = 0x8000_0000;
const GENERIC_WRITE = 0x4000_0000;
const FILE_READ_ATTRIBUTES = 0x0000_0080;
const FILE_SHARE_READ = 0x0000_0001;
const FILE_SHARE_WRITE = 0x0000_0002;
const CREATE_NEW = 1;
const OPEN_EXISTING = 3;
const FILE_ATTRIBUTE_DIRECTORY = 0x0000_0010;
const FILE_ATTRIBUTE_REPARSE_POINT = 0x0000_0400;
const FILE_FLAG_OPEN_REPARSE_POINT = 0x0020_0000;
const FILE_FLAG_BACKUP_SEMANTICS = 0x0200_0000;

interface WindowsFileInfo {
  identity: AnchoredFileIdentity;
  attributes: number;
  links: number;
  size: bigint;
}

interface WindowsChain {
  handles: WindowsHandle[];
  identities: AnchoredFileIdentity[];
}

function widePath(value: string): Buffer {
  return Buffer.from(`${path.toNamespacedPath(value)}\0`, "utf16le");
}

function validWindowsHandle(handle: Pointer | null): handle is Pointer {
  return typeof handle === "number" && Number.isSafeInteger(handle) && handle > 0;
}

function closeWindowsHandles(runtime: WindowsRuntime, handles: readonly WindowsHandle[]): void {
  for (const handle of [...handles].reverse()) {
    try {
      runtime.symbols.CloseHandle(handle);
    } catch {
      // The primary authority result must not be replaced by cleanup noise.
    }
  }
}

function openWindowsHandle(
  runtime: WindowsRuntime,
  target: string,
  access: number,
  share: number,
  disposition: number,
  flags: number,
): WindowsHandle {
  const encoded = widePath(target);
  const handle = runtime.symbols.CreateFileW(
    runtime.ptr(encoded),
    access,
    share,
    null,
    disposition,
    flags,
    null,
  );
  if (!validWindowsHandle(handle)) {
    throw new AnchoredFileAuthorityError("target_unavailable");
  }
  return handle;
}

function windowsInfo(runtime: WindowsRuntime, handle: WindowsHandle): WindowsFileInfo {
  const buffer = Buffer.alloc(64);
  if (!runtime.symbols.GetFileInformationByHandle(handle, runtime.ptr(buffer))) {
    throw new AnchoredFileAuthorityError("containment_changed");
  }
  const attributes = buffer.readUInt32LE(0);
  const volume = buffer.readUInt32LE(28);
  const size = (BigInt(buffer.readUInt32LE(32)) << 32n) | BigInt(buffer.readUInt32LE(36));
  const links = buffer.readUInt32LE(40);
  const file = (BigInt(buffer.readUInt32LE(44)) << 32n) | BigInt(buffer.readUInt32LE(48));
  return {
    identity: { volume: String(volume), file: String(file) },
    attributes,
    links,
    size,
  };
}

function openWindowsChain(
  runtime: WindowsRuntime,
  root: string,
  directories: readonly string[],
): WindowsChain {
  const handles: WindowsHandle[] = [];
  const identities: AnchoredFileIdentity[] = [];
  try {
    let cursor = root;
    for (let index = 0; index <= directories.length; index += 1) {
      if (index > 0) cursor = path.join(cursor, directories[index - 1]!);
      const handle = openWindowsHandle(
        runtime,
        cursor,
        FILE_READ_ATTRIBUTES,
        FILE_SHARE_READ | FILE_SHARE_WRITE,
        OPEN_EXISTING,
        FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT,
      );
      handles.push(handle);
      const info = windowsInfo(runtime, handle);
      if (
        !(info.attributes & FILE_ATTRIBUTE_DIRECTORY) ||
        info.attributes & FILE_ATTRIBUTE_REPARSE_POINT
      ) {
        throw new AnchoredFileAuthorityError("containment_changed");
      }
      identities.push(info.identity);
    }
    return { handles, identities };
  } catch (error) {
    closeWindowsHandles(runtime, handles);
    if (error instanceof AnchoredFileAuthorityError) throw error;
    throw new AnchoredFileAuthorityError("containment_changed");
  }
}

function sameWindowsChain(left: WindowsChain, right: WindowsChain): boolean {
  return (
    left.identities.length === right.identities.length &&
    left.identities.every((identity, index) => sameIdentity(identity, right.identities[index]!))
  );
}

function checkedWindowsTarget(runtime: WindowsRuntime, handle: WindowsHandle): WindowsFileInfo {
  const info = windowsInfo(runtime, handle);
  if (
    info.attributes & FILE_ATTRIBUTE_DIRECTORY ||
    info.attributes & FILE_ATTRIBUTE_REPARSE_POINT ||
    info.links !== 1
  ) {
    throw new AnchoredFileAuthorityError("containment_changed");
  }
  return info;
}

function setWindowsOffsetZero(runtime: WindowsRuntime, handle: WindowsHandle): void {
  if (!runtime.symbols.SetFilePointerEx(handle, 0n, null, 0)) {
    throw new AnchoredFileAuthorityError("mutation_failed");
  }
}

function readWindowsContent(
  runtime: WindowsRuntime,
  handle: WindowsHandle,
  size: bigint,
): Buffer {
  if (size > BigInt(MAX_AUTHORITY_CONTENT_BYTES)) {
    throw new AnchoredFileAuthorityError("content_changed");
  }
  setWindowsOffsetZero(runtime, handle);
  const content = Buffer.alloc(Number(size));
  if (content.length === 0) return content;
  const read = Buffer.alloc(4);
  if (!runtime.symbols.ReadFile(handle, runtime.ptr(content), content.length, runtime.ptr(read), null)) {
    throw new AnchoredFileAuthorityError("mutation_failed");
  }
  if (read.readUInt32LE(0) !== content.length) {
    throw new AnchoredFileAuthorityError("content_changed");
  }
  return content;
}

function writeWindowsContent(
  runtime: WindowsRuntime,
  handle: WindowsHandle,
  content: Buffer,
): void {
  setWindowsOffsetZero(runtime, handle);
  if (!runtime.symbols.SetEndOfFile(handle)) {
    throw new AnchoredFileAuthorityError("mutation_failed");
  }
  if (content.length > 0) {
    const written = Buffer.alloc(4);
    if (!runtime.symbols.WriteFile(handle, runtime.ptr(content), content.length, runtime.ptr(written), null)) {
      throw new AnchoredFileAuthorityError("mutation_failed");
    }
    if (written.readUInt32LE(0) !== content.length) {
      throw new AnchoredFileAuthorityError("mutation_failed");
    }
  }
  if (!runtime.symbols.FlushFileBuffers(handle)) {
    throw new AnchoredFileAuthorityError("mutation_failed");
  }
}

async function runWindowsAuthority(
  request: AnchoredFileAuthorityRequest,
  canonicalRoot: string,
  content: Buffer,
): Promise<AnchoredFileProof> {
  const runtime = await windowsRuntime();
  const directories = request.components.slice(0, -1);
  const target = path.join(canonicalRoot, ...request.components);
  const heldHandles: WindowsHandle[] = [];
  try {
    const first = openWindowsChain(runtime, canonicalRoot, directories);
    heldHandles.push(...first.handles);
    let firstTargetHandle: WindowsHandle | null = null;
    let firstTarget: WindowsFileInfo | null = null;
    if (request.operation !== "create") {
      firstTargetHandle = openWindowsHandle(
        runtime,
        target,
        (GENERIC_READ | FILE_READ_ATTRIBUTES) >>> 0,
        FILE_SHARE_READ | FILE_SHARE_WRITE,
        OPEN_EXISTING,
        FILE_FLAG_OPEN_REPARSE_POINT,
      );
      heldHandles.push(firstTargetHandle);
      firstTarget = checkedWindowsTarget(runtime, firstTargetHandle);
      if (
        request.expected &&
        (!sameIdentity(first.identities[0]!, request.expected.root) ||
          !sameIdentity(firstTarget.identity, request.expected.file))
      ) {
        throw new AnchoredFileAuthorityError("identity_changed");
      }
    }

    await request.lifecycle?.beforeFinalTraversal?.();

    const finalChain = openWindowsChain(runtime, canonicalRoot, directories);
    heldHandles.push(...finalChain.handles);
    if (!sameWindowsChain(first, finalChain)) {
      throw new AnchoredFileAuthorityError("containment_changed");
    }

    const finalTargetHandle = openWindowsHandle(
      runtime,
      target,
      request.operation === "verify"
        ? (GENERIC_READ | FILE_READ_ATTRIBUTES) >>> 0
        : (GENERIC_READ | GENERIC_WRITE | FILE_READ_ATTRIBUTES) >>> 0,
      FILE_SHARE_READ,
      request.operation === "create" ? CREATE_NEW : OPEN_EXISTING,
      FILE_FLAG_OPEN_REPARSE_POINT,
    );
    heldHandles.push(finalTargetHandle);
    const finalTarget = checkedWindowsTarget(runtime, finalTargetHandle);
    if (firstTarget && !sameIdentity(firstTarget.identity, finalTarget.identity)) {
      throw new AnchoredFileAuthorityError("identity_changed");
    }
    if (
      request.expected &&
      (!sameIdentity(finalChain.identities[0]!, request.expected.root) ||
        !sameIdentity(finalTarget.identity, request.expected.file))
    ) {
      throw new AnchoredFileAuthorityError("identity_changed");
    }

    let contentSha256: string;
    if (request.operation === "verify") {
      contentSha256 = digest(readWindowsContent(runtime, finalTargetHandle, finalTarget.size));
      if (!sameProof(
        {
          root: finalChain.identities[0]!,
          file: finalTarget.identity,
          contentSha256,
        },
        request.expected!,
      )) {
        throw new AnchoredFileAuthorityError("content_changed");
      }
    } else {
      if (request.expected) {
        const existing = readWindowsContent(runtime, finalTargetHandle, finalTarget.size);
        if (digest(existing) !== request.expected.contentSha256) {
          throw new AnchoredFileAuthorityError("content_changed");
        }
      }
      writeWindowsContent(runtime, finalTargetHandle, content);
      const after = checkedWindowsTarget(runtime, finalTargetHandle);
      if (!sameIdentity(after.identity, finalTarget.identity)) {
        throw new AnchoredFileAuthorityError("identity_changed");
      }
      contentSha256 = digest(content);
    }

    return {
      root: finalChain.identities[0]!,
      file: finalTarget.identity,
      contentSha256,
    };
  } catch (error) {
    if (error instanceof AnchoredFileAuthorityError) throw error;
    throw new AnchoredFileAuthorityError("mutation_failed");
  } finally {
    closeWindowsHandles(runtime, heldHandles);
  }
}

/** Execute one root-anchored create, replacement, or proof verification. */
export async function runAnchoredFileAuthority(
  request: AnchoredFileAuthorityRequest,
): Promise<AnchoredFileProof> {
  const content = validateRequest(request);
  const canonicalRoot = await realpath(request.root).catch(() => null);
  if (!canonicalRoot || !samePath(canonicalRoot, request.root)) {
    throw new AnchoredFileAuthorityError("invalid_request");
  }
  if (process.platform === "win32") {
    return runWindowsAuthority(request, canonicalRoot, content);
  }
  if (process.platform === "linux" || process.platform === "darwin") {
    return runPosixAuthority(request, canonicalRoot, content);
  }
  throw new AnchoredFileAuthorityError("unsupported_platform");
}
