// Browser-safe GitHub transport for Jikko Git snapshots.
// Uses GitHub's Git Database REST API rather than Git smart-HTTP, avoiding
// the CORS proxy required by browser isomorphic-git for github.com.
export class GitHubRemote {
  constructor({owner, repo, token}) { this.owner=owner; this.repo=repo; this.token=token; }
  static fromURL(url, token) {
    const m=String(url).match(/^https:\/\/github\.com\/([^/]+)\/([^/#]+?)(?:\.git)?$/);
    if(!m) throw new Error("Expected a github.com repository URL");
    return new GitHubRemote({owner:m[1],repo:m[2],token});
  }
  async branches(prefix="") {
    const xs=await this.api(`/repos/${this.owner}/${this.repo}/git/matching-refs/heads/${encodeURIComponent(prefix)}`,{},[200,409]);
    return (Array.isArray(xs)?xs:[]).map(x=>x.ref.replace("refs/heads/",""));
  }
  async readBranch(ref) {
    const commit=await this.api(`/repos/${this.owner}/${this.repo}/git/commits/${encodeURIComponent(ref)}`);
    const tree=await this.api(`/repos/${this.owner}/${this.repo}/git/trees/${commit.tree.sha}?recursive=1`);
    if(tree.truncated) throw new Error("Remote tree is too large for browser snapshot restore");
    const files={};
    for(const item of tree.tree||[]) if(item.type==="blob") {
      const blob=await this.api(`/repos/${this.owner}/${this.repo}/git/blobs/${item.sha}`);
      files[item.path]=decodeBlob(blob);
    }
    return {sha:commit.sha,files};
  }
  async writeBranch(ref, files, {message="Jikko browser sync", expectedHead=null}={}) {
    let head=null, baseTree=null;
    try {
      const current=await this.api(`/repos/${this.owner}/${this.repo}/git/ref/heads/${encodeURIComponent(ref)}`);
      head=current.object.sha;
      if(expectedHead && head!==expectedHead) throw new Error("remote branch diverged");
      const parent=await this.api(`/repos/${this.owner}/${this.repo}/git/commits/${head}`);
      baseTree=parent.tree.sha;
    } catch(e) { if(e.status!==404) throw e; }
    const treeEntries=Object.entries(files).map(([path,content])=>({path,mode:"100644",type:"blob",content:typeof content==="string"?content:new TextDecoder().decode(content)}));
    const body={tree:treeEntries}; if(baseTree) body.base_tree=baseTree;
    const tree=await this.api(`/repos/${this.owner}/${this.repo}/git/trees`,{method:"POST",body});
    const commit=await this.api(`/repos/${this.owner}/${this.repo}/git/commits`,{method:"POST",body:{message,tree:tree.sha,parents:head?[head]:[]}});
    if(head) await this.api(`/repos/${this.owner}/${this.repo}/git/refs/heads/${encodeURIComponent(ref)}`,{method:"PATCH",body:{sha:commit.sha,force:false}});
    else await this.api(`/repos/${this.owner}/${this.repo}/git/refs`,{method:"POST",body:{ref:"refs/heads/"+ref,sha:commit.sha}});
    return commit.sha;
  }
  async api(path,{method="GET",body}={},ok=[200,201]) {
    const r=await fetch("https://api.github.com"+path,{method,headers:{Accept:"application/vnd.github+json","X-GitHub-Api-Version":"2022-11-28",Authorization:"Bearer "+this.token,...(body?{"Content-Type":"application/json"}:{})},body:body?JSON.stringify(body):undefined});
    if(!ok.includes(r.status)){const e=new Error("GitHub API "+r.status);e.status=r.status;throw e}
    return r.status===204?null:r.json();
  }
}
function decodeBlob(blob){
  if(blob.encoding!=="base64") throw new Error("Unsupported GitHub blob encoding");
  const binary=atob(String(blob.content).replace(/\n/g,""));const out=new Uint8Array(binary.length);
  for(let i=0;i<binary.length;i++)out[i]=binary.charCodeAt(i);return out;
}
