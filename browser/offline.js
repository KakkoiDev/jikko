import { BrowserGit } from "./git.js";

// OfflineBackend is the browser equivalent of the native Go backend.
// HTMX remains the UI layer; this object supplies the local operations that
// an offline transport maps to the same application actions.
export class OfflineBackend {
  constructor(git, wasmHandle, actor = "") {
    this.git = git;
    this.handle = wasmHandle;
    this.actor = actor;
  }

  static async open({actor = "", root = "/jikko", dir = "/workspace"} = {}) {
    const git = await BrowserGit.open({root, dir});
    await git.ensureRepository();
    const files = await readWorkspace(git.fs, dir);
    const result = JikkoWASM.open(files);
    if (!result.ok) throw new Error(result.error);
    return new OfflineBackend(git, result.value, actor);
  }

  tree() { return decode(JikkoWASM.tree(this.handle, this.actor)); }
  readMany(refs) { return decode(JikkoWASM.readMany(this.handle, this.actor, refs)); }
  read(ref) { return this.readMany([ref])[0]; }
  mentions() { return decode(JikkoWASM.mentions(this.handle, this.actor)); }

  async pages({type = "", status = ""} = {}) {
    const entries = this.tree().filter(entry => !type || entry.type === type);
    const refs = entries.map(entry => entry.path.replace(/\\.md$/, ""));
    if (!refs.length) return [];
    return this.readMany(refs).filter(page => !status || String(page.metadata?.status ?? "") === status);
  }

  exportZIP() {
    const result = JikkoWASM.exportZIP(this.handle);
    if (!result.ok) throw new Error(result.error);
    return new Blob([result.value], {type: "application/zip"});
  }

  async commit(message, operation = "workspace-mutation") {
    return this.git.commit({message, actor: this.actor, operation});
  }

  log(depth) { return this.git.log(depth); }
  status() { return this.git.diffStatus(); }
  branches() { return this.git.branches(); }
  branch(ref) { return this.git.branch(ref); }
  checkout(ref) { return this.git.checkout(ref); }
  merge(ref) { return this.git.merge(ref); }
  currentBranch() { return this.git.currentBranch(); }
  remoteBranches(remote) { return this.git.remoteBranches(remote); }
  addRemote(options) { return this.git.addRemote(options); }
  fetch(options) { return this.git.fetch(options); }
  pull(options) { return this.git.pull(options); }
  push(options) { return this.git.push(options); }
  syncCurrentBranch(options) { return this.git.syncCurrentBranch(options); }
}

async function readWorkspace(fs, root) {
  const files = {};
  await walk(fs, root, "", files);
  return files;
}

async function walk(fs, absolute, relative, files) {
  for (const name of await fs.promises.readdir(absolute)) {
    if (name === ".git" || name === ".data" || name === ".auth.md") continue;
    const abs = absolute + "/" + name;
    const rel = relative ? relative + "/" + name : name;
    const stat = await fs.promises.stat(abs);
    if (stat.isDirectory()) await walk(fs, abs, rel, files);
    else files[rel] = new Uint8Array(await fs.promises.readFile(abs));
  }
}

function decode(result) {
  if (!result.ok) throw new Error(result.error);
  return JSON.parse(result.json);
}
