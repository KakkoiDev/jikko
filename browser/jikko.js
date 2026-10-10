// Browser storage adapter for Jikko WASM.
// OPFS owns durable bytes; Jikko WASM owns parsing, permissions and navigation semantics.
//
// The browser runtime is single-user local mode: the host application binds
// the individual Identity it vouches for when it opens the workspace, and
// every call acts as that Identity. Omit it to act anonymously.

export async function loadOPFS(root, {identity = ""} = {}) {
  root = root || await navigator.storage.getDirectory();
  const files = {};
  await walk(root, "", files);
  return JikkoWASM.open(files, {identity});
}

async function walk(dir, prefix, files) {
  for await (const [name, handle] of dir.entries()) {
    if (!prefix && (name === ".git" || name === ".data" || name === ".auth.md")) continue;
    const path = prefix ? prefix + "/" + name : name;
    if (handle.kind === "directory") {
      await walk(handle, path, files);
    } else {
      const file = await handle.getFile();
      files[path] = new Uint8Array(await file.arrayBuffer());
    }
  }
}

export function tree(handle) {
  return decode(JikkoWASM.tree(handle));
}

export function read(handle, ref) {
  return readMany(handle, [ref])[0];
}

export function readMany(handle, refs) {
  return decode(JikkoWASM.readMany(handle, refs));
}

export function mentions(handle) {
  return decode(JikkoWASM.mentions(handle));
}

export function view(handle, ref) {
  return decode(JikkoWASM.view(handle, ref));
}

export function check(handle) {
  return decode(JikkoWASM.check(handle));
}

export function identity(handle) {
  return decode(JikkoWASM.identity(handle));
}

// render returns the page's Markdown rendered to safe HTML by the Go core.
export function render(handle, ref) {
  const result = JikkoWASM.render(handle, ref);
  if (!result.ok) throw new Error(result.error);
  return result.value;
}

export function exportWorkspaceZIP(handle) {
  const result = JikkoWASM.exportZIP(handle);
  if (!result.ok) throw new Error(result.error);
  return new Blob([result.value], {type: "application/zip"});
}

export function downloadWorkspaceZIP(handle, filename = "jikko-workspace.zip") {
  const url = URL.createObjectURL(exportWorkspaceZIP(handle));
  const a = document.createElement("a");
  a.href = url; a.download = filename; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

function decode(result) {
  if (!result.ok) throw new Error(result.error);
  return JSON.parse(result.json);
}
