import git from "isomorphic-git";
import http from "isomorphic-git/http/web";
import { VFSFileSystem } from "@componentor/fs";

export class BrowserGit {
  constructor(fs, dir = "/workspace") {
    this.fs = fs;
    this.dir = dir;
  }

  static async open({root = "/jikko", dir = "/workspace"} = {}) {
    const fs = new VFSFileSystem({root});
    await fs.init();
    await fs.promises.mkdir(dir, {recursive: true});
    return new BrowserGit(fs, dir);
  }

  async ensureRepository(defaultBranch = "master") {
    try {
      await this.fs.promises.stat(this.dir + "/.git");
    } catch {
      await git.init({fs: this.fs, dir: this.dir, defaultBranch});
    }
  }

  async statusMatrix() {
    return git.statusMatrix({fs: this.fs, dir: this.dir});
  }

  async addAll() {
    for (const row of await this.statusMatrix()) {
      const [filepath, head, workdir] = row;
      if (workdir === 0) {
        await git.remove({fs: this.fs, dir: this.dir, filepath});
      } else if (head !== workdir) {
        await git.add({fs: this.fs, dir: this.dir, filepath});
      }
    }
  }

  async commit({message, actor, operation = "workspace-mutation", author}) {
    // Both values are written into the trailer block. A line break or other
    // whitespace would let a caller append a forged Jikko-Actor trailer, as
    // the native harness also refuses.
    for (const [name, value] of [["operation", operation], ["actor", actor || "browser"]]) {
      if (!value || /[\s\p{Cc}]/u.test(value)) throw new Error(`${name} ${JSON.stringify(value)} must be a single word`);
    }
    await this.ensureRepository();
    await this.addAll();
    const identity = author || {
      name: actor || "Jikko Browser",
      email: "browser@jikko.local"
    };
    const trailers = [
      "",
      "Jikko-Actor: " + (actor || "browser"),
      "Jikko-Operation: " + operation
    ].join("\n");
    return git.commit({
      fs: this.fs,
      dir: this.dir,
      message: message + trailers,
      author: identity
    });
  }

  async log(depth = 100) {
    try {
      return await git.log({fs: this.fs, dir: this.dir, depth});
    } catch {
      return [];
    }
  }

  async branches() {
    return git.listBranches({fs: this.fs, dir: this.dir});
  }

  async remoteBranches(remote = "origin") {
    return git.listBranches({fs: this.fs, dir: this.dir, remote});
  }

  async branch(ref) {
    return git.branch({fs: this.fs, dir: this.dir, ref});
  }

  async checkout(ref) {
    return git.checkout({fs: this.fs, dir: this.dir, ref});
  }

  async currentBranch() {
    return git.currentBranch({fs: this.fs, dir: this.dir});
  }

  async merge(theirs, author = {name: "Jikko Browser", email: "browser@jikko.local"}) {
    return git.merge({fs: this.fs, dir: this.dir, ours: await this.currentBranch(), theirs, author});
  }

  async addRemote({remote = "origin", url, force = false}) {
    return git.addRemote({fs: this.fs, dir: this.dir, remote, url, force});
  }

  async fetch({remote = "origin", ref, depth, singleBranch = true, auth}) {
    return git.fetch({
      fs: this.fs, http, dir: this.dir, remote, ref, depth, singleBranch,
      onAuth: authCallback(auth)
    });
  }

  async pull({remote = "origin", ref, author, auth}) {
    const branch = ref || await this.currentBranch();
    if (!branch) throw new Error("cannot pull without a checked-out branch");
    return git.pull({
      fs: this.fs, http, dir: this.dir, remote, ref: branch,
      author: author || {name: "Jikko Browser", email: "browser@jikko.local"},
      singleBranch: true, onAuth: authCallback(auth)
    });
  }

  async push({remote = "origin", ref, remoteRef, force = false, auth}) {
    const branch = ref || await this.currentBranch();
    if (!branch) throw new Error("cannot push without a checked-out branch");
    return git.push({
      fs: this.fs, http, dir: this.dir, remote, ref: branch,
      remoteRef: remoteRef || branch, force, onAuth: authCallback(auth)
    });
  }

  async syncCurrentBranch({remote = "origin", auth, author}) {
    const ref = await this.currentBranch();
    if (!ref) throw new Error("cannot sync without a checked-out branch");
    await this.fetch({remote, ref, singleBranch: true, auth});
    await this.pull({remote, ref, author, auth});
    return this.push({remote, ref, auth});
  }

  async diffStatus() {
    return this.statusMatrix();
  }
}

function authCallback(auth) {
  if (!auth) return undefined;
  if (typeof auth === "function") return auth;
  return () => auth;
}
