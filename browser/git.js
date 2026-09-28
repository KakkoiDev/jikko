import git from "isomorphic-git";
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

  async branch(ref) {
    return git.branch({fs: this.fs, dir: this.dir, ref});
  }

  async checkout(ref) {
    return git.checkout({fs: this.fs, dir: this.dir, ref});
  }

  async merge(theirs, author = {name: "Jikko Browser", email: "browser@jikko.local"}) {
    return git.merge({fs: this.fs, dir: this.dir, ours: await git.currentBranch({fs: this.fs, dir: this.dir}), theirs, author});
  }

  async diffStatus() {
    return this.statusMatrix();
  }
}
