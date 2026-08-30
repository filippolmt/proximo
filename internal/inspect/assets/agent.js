// proximo Inspection agent. Served concatenated after the vendored
// @sentry/browser bundle, so `Sentry` is already on the page by the time this
// runs and `document.currentScript` is the tag the hop injected.
//
// The SDK does the work — exception and rejection handlers, console/fetch/XHR/
// click/navigation breadcrumbs, stack parsing. This adds the two things it has
// no way to know about: where to report (the tunnel carries the Exchange id, so
// the two halves join server-side) and the DOM Snapshot, which Sentry only
// captures as part of its replay product.
(function () {
  try {
    var tag = document.currentScript;
    var id = tag && tag.getAttribute("data-proximo-exchange");
    if (!id || typeof Sentry === "undefined") return;

    Sentry.init({
      // The tunnel decides where envelopes go; the DSN only has to be
      // well-formed for the SDK to build one.
      dsn: "https://proximo@proximo.invalid/1",
      tunnel: "/.proximo/ingest?x=" + encodeURIComponent(id),
      // Local development: record everything, filter at display time.
      sampleRate: 1.0,
      maxBreadcrumbs: 100,
      // Nothing leaves the machine, so there is nothing to scrub for.
      sendDefaultPii: true,
      beforeSend: function (event, hint) {
        try {
          hint = hint || {};
          hint.attachments = (hint.attachments || []).concat([
            {
              filename: "dom.html",
              data: document.documentElement.outerHTML,
              contentType: "text/html",
            },
          ]);
        } catch (e) {
          // A page that will not yield its DOM still deserves its report.
        }
        return event;
      },
    });
  } catch (e) {
    // Inspection must never be the reason a page fails to run.
  }
})();
