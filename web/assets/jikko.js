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

// Leaving the page closes its live streams first. The browser would abort
// their fetches itself a moment later, but htmx then reports the aborted
// stream as an error on the console; closing them here ends each stream
// cleanly. The connection record is where the htmx SSE extension keeps it.
window.addEventListener("pagehide", function () {
  document.querySelectorAll("[hx-sse\\:connect]").forEach(function (element) {
    var sse = element._htmx && element._htmx.sse;
    if (!sse) return;
    if (sse.abortController && sse.abortController.abort) sse.abortController.abort();
    if (sse.reader && sse.reader.cancel) sse.reader.cancel();
  });
});
