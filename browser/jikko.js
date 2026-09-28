// Browser storage adapter for Jikko WASM.
// OPFS owns durable bytes; Jikko WASM owns parsing, permissions and navigation semantics.

export async function loadOPFS(root = await navigator.storage.getDirectory()) {
  const files = {};
  await walk(root, "", files);
  return JikkoWASM.open(files);
}

async function walk(dir, prefix, files) {
  for await (const [name, handle] of dir.entries()) {
    const path = prefix ? prefix + "/" + name : name;
    if (handle.kind === "directory") {
      await walk(handle, path, files);
    } else {
      const file = await handle.getFile();
      files[path] = new Uint8Array(await file.arrayBuffer());
    }
  }
}

export function tree(handle, actor = "") {
  return decode(JikkoWASM.tree(handle, actor));
}

export function read(handle, actor, ref) {
  const pages = readMany(handle, actor, [ref]);
  return pages[0];
}

export function readMany(handle, actor, refs) {
  return decode(JikkoWASM.readMany(handle, actor, refs));
}

export function mentions(handle, actor) {
  return decode(JikkoWASM.mentions(handle, actor));
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
