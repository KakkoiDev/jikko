// Progressive enhancement for the Jikko browser interface. Everything here is
// optional: every form works without it, and it holds no Jikko semantics.
//
// Selecting text in a page body copies it into the new-comment form, so a
// comment can be anchored by highlighting the text it discusses.
document.addEventListener("selectionchange", function () {
  var selection = document.getSelection();
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return;
  var range = selection.getRangeAt(0);
  var node = range.commonAncestorContainer;
  var element = node.nodeType === 1 ? node : node.parentElement;
  if (!element || !element.closest(".jk-body")) return;
  // The comment ids shown after existing anchors are not page text; the
  // server maps the rendered text back to the Markdown source, across bold,
  // links and references.
  var copy = range.cloneContents();
  copy.querySelectorAll(".jk-anchor-ref").forEach(function (ref) { ref.remove(); });
  var text = (copy.textContent || "").replace(/\s+/g, " ").trim();
  var field = document.getElementById("jk-anchor");
  if (field && text) field.value = text;
});

// The search box on the workspace list narrows the cards to those whose text
// contains every word typed. The list is re-rendered by a live stream, so the
// filter is re-applied after each swap.
function applyFilter() {
  var box = document.getElementById("jk-search");
  if (!box) return;
  var words = box.value.toLowerCase().split(/\s+/).filter(Boolean);
  document.querySelectorAll("#pages .jk-card").forEach(function (card) {
    var text = card.textContent.toLowerCase();
    var show = words.every(function (w) { return text.indexOf(w) >= 0; });
    card.hidden = !show;
  });
}
document.addEventListener("input", function (event) {
  if (event.target && event.target.id === "jk-search") applyFilter();
});
document.addEventListener("htmx:afterSwap", applyFilter);
document.addEventListener("htmx:sseMessage", applyFilter);

// When a swap removes an element with a live stream (the page list, or the
// change notice replacing itself), the htmx SSE extension aborts the stream's
// fetch during its cleanup, and the read it had in flight rejects with an
// AbortError that nothing awaits. The stream is closed on purpose, so that
// rejection is noise; everything else still reaches the console.
window.addEventListener("unhandledrejection", function (event) {
  var reason = event.reason;
  if (!reason || reason.name !== "AbortError") return;
  var stack = String(reason.stack || "");
  if (stack.indexOf("htmx") < 0 && stack.indexOf("htmx_before_cleanup") < 0) return;
  event.preventDefault();
});
