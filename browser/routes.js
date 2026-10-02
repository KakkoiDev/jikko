// Shared offline route behavior for the existing HTMX page list.
// Native mode gets this fragment from Go's /pages handler; offline mode renders
// the same contract from Jikko WASM data.
export const offlineRoutes = {
  "/pages": async (backend, parameters) => backend.pages({
    type: value(parameters, "type"),
    status: value(parameters, "status")
  })
};

export function renderOfflineRoute(path, result) {
  if (path !== "/pages") throw new Error("no offline renderer for " + path);
  return `<section id="pages"><div class="item-group">${result.map(page =>
    `<article class="item" data-variant="outline"><section><h3>${escapeHTML(page.title)}</h3><p class="text-muted-foreground">${escapeHTML(page.type)} · ${escapeHTML(page.path)}</p></section></article>`
  ).join("") || '<article class="item" data-variant="outline"><section><p>No pages.</p></section></article>'}</div></section>`;
}

function value(parameters, key) {
  if (!parameters) return "";
  if (typeof parameters.get === "function") return parameters.get(key) || "";
  return parameters[key] || "";
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, char => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  })[char]);
}
