// Install a transport hook for the offline HTMX harness.
//
// The existing HTMX templates remain the frontend contract. In native mode
// requests go to the Go HTTP server. In offline mode application code handles
// the same actions through OfflineBackend and swaps the returned/rendered
// fragment into the requested target. This module intentionally contains no
// Jikko semantics.
//
// render(action, result, element) must return an HTML string.
export function installOfflineHTMX({backend, routes, render}) {
  document.body.addEventListener("htmx:configRequest", async event => {
    if (!document.documentElement.hasAttribute("data-jikko-offline")) return;

    const path = new URL(event.detail.path, location.href).pathname;
    const route = routes[path];
    if (!route) return;

    event.preventDefault();
    const result = await route(backend, event.detail.parameters, event.detail.elt);
    const html = await render(path, result, event.detail.elt);
    const target = htmx.find(event.detail.elt.getAttribute("hx-target")) || event.detail.elt;
    htmx.swap(target, html, {swapStyle: event.detail.elt.getAttribute("hx-swap") || "innerHTML"});
  });
}
