// Progressive enhancement for the Jikko browser interface. Everything here is
// optional: every form works without it, and it holds no Jikko semantics.
//
// Selecting text in a page body copies it into the new-comment form, so a
// comment can be anchored by highlighting the text it discusses.
document.addEventListener("selectionchange", function () {
  var selection = document.getSelection();
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return;
  var node = selection.getRangeAt(0).commonAncestorContainer;
  var element = node.nodeType === 1 ? node : node.parentElement;
  if (!element || !element.closest(".jk-body")) return;
  var text = selection.toString().replace(/\s+/g, " ").trim();
  var field = document.getElementById("jk-anchor");
  if (field && text && text.indexOf("\n") < 0) field.value = text;
});

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
