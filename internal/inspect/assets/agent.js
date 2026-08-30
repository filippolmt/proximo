// proximo Inspection agent. Injected into HTML responses of routes labelled
// proximo.inspect, and served from the page's own origin, so it reports
// same-origin and nothing here needs CORS.
//
// It is deliberately ours rather than a vendored SDK. The one thing a real SDK
// buys — normalising errors across engines — is not something proximo needs:
// `window.onerror` already hands over the message, the file, the line, the column
// and the Error object, and `error.stack` is a string the browser already
// formatted. The consumer is a person or an agent reading text, so the honest
// move is to pass that text through rather than take on a build step, a
// committed bundle and a wire format we do not own. Chrome is the supported
// browser; see docs/observability.md.
(function () {
  "use strict";

  var tag = document.currentScript;
  var exchange = tag && tag.getAttribute("data-proximo-exchange");
  if (!exchange) return;

  var ENDPOINT = "/.proximo/ingest?x=" + encodeURIComponent(exchange);
  var MAX_BREADCRUMBS = 100;
  var MAX_VALUE = 500; // per breadcrumb message, so one huge log cannot dominate

  var breadcrumbs = [];
  var sentDOM = false;
  var lastKey = "";

  function now() {
    return Date.now() / 1000;
  }

  function clip(s) {
    s = String(s);
    return s.length > MAX_VALUE ? s.slice(0, MAX_VALUE) + "…" : s;
  }

  function crumb(category, level, message) {
    breadcrumbs.push({ at: now(), category: category, level: level, message: clip(message) });
    if (breadcrumbs.length > MAX_BREADCRUMBS) breadcrumbs.shift();
  }

  // send never throws and never blocks the page. sendBeacon survives the
  // unload that often follows a fatal error; fetch with keepalive is the
  // fallback for the payloads beacon refuses (it caps at ~64 KB, and a DOM
  // snapshot is bigger than that).
  function send(report) {
    try {
      var body = JSON.stringify(report);
      if (!report.dom && navigator.sendBeacon &&
          navigator.sendBeacon(ENDPOINT, new Blob([body], { type: "application/json" }))) {
        return;
      }
      fetch(ENDPOINT, {
        method: "POST",
        body: body,
        keepalive: true,
        headers: { "Content-Type": "application/json" },
      }).catch(function () {});
    } catch (e) {
      // Inspection must never be the reason a page fails.
    }
  }

  function report(fields) {
    // Two identical errors in a row are one story, not two — a render loop can
    // otherwise fill the whole buffer with the same line.
    var key = fields.type + "|" + fields.message + "|" + fields.line;
    if (key === lastKey) return;
    lastKey = key;

    fields.at = now();
    fields.breadcrumbs = breadcrumbs.slice();
    if (!sentDOM) {
      try {
        fields.dom = document.documentElement.outerHTML;
        sentDOM = true;
      } catch (e) {
        // A page that will not yield its DOM still deserves its report.
      }
    }
    send(fields);
  }

  // --- what went wrong ------------------------------------------------------

  window.addEventListener("error", function (ev) {
    // A failed subresource raises the same event with no Error attached: the
    // target is the element that could not load.
    if (ev.target && ev.target !== window && ev.target.tagName) {
      var url = ev.target.src || ev.target.href;
      crumb("resource", "error", ev.target.tagName.toLowerCase() + " failed to load: " + url);
      return;
    }
    var err = ev.error;
    report({
      type: (err && err.name) || "Error",
      level: "error",
      message: (err && err.message) || String(ev.message || "Unknown error"),
      file: ev.filename || null,
      line: ev.lineno || 0,
      col: ev.colno || 0,
      stack: (err && err.stack) || null,
    });
  }, true);

  window.addEventListener("unhandledrejection", function (ev) {
    var r = ev.reason;
    report({
      type: (r && r.name) || "UnhandledRejection",
      level: "error",
      message: (r && r.message) || String(r),
      stack: (r && r.stack) || null,
    });
  });

  // A blocked script or request raises no exception — the browser fires this
  // instead, and without it a policy violation is exactly the silent failure
  // Inspection exists to surface.
  window.addEventListener("securitypolicyviolation", function (ev) {
    report({
      type: "SecurityPolicyViolation",
      level: "warning",
      message: "Content-Security-Policy blocked " + (ev.blockedURI || "an inline resource") +
        " (" + (ev.effectiveDirective || ev.violatedDirective) + ")",
      file: ev.sourceFile || null,
      line: ev.lineNumber || 0,
    });
  });

  // --- what happened before it ----------------------------------------------

  ["log", "info", "warn", "error", "debug"].forEach(function (level) {
    var original = console[level];
    if (typeof original !== "function") return;
    console[level] = function () {
      try {
        crumb("console", level === "log" ? "info" : level,
          Array.prototype.map.call(arguments, function (a) {
            if (a instanceof Error) return a.message;
            if (typeof a === "object") { try { return JSON.stringify(a); } catch (e) { return "[object]"; } }
            return String(a);
          }).join(" "));
      } catch (e) {
        // never let instrumentation break a console call
      }
      return original.apply(console, arguments);
    };
  });

  var origFetch = window.fetch;
  if (typeof origFetch === "function") {
    window.fetch = function (input, init) {
      var method = (init && init.method) || (input && input.method) || "GET";
      var url = (input && input.url) || String(input);
      // A request proximo itself made is not part of the page's story.
      if (url.indexOf("/.proximo/") !== -1) return origFetch.apply(this, arguments);
      return origFetch.apply(this, arguments).then(function (res) {
        crumb("fetch", res.ok ? "info" : "error", method + " " + url + " → " + res.status);
        return res;
      }, function (err) {
        // Never reached the network: the one request half the proxy cannot see.
        crumb("fetch", "error", method + " " + url + " → " + (err && err.message ? err.message : "failed"));
        throw err;
      });
    };
  }

  var origOpen = XMLHttpRequest.prototype.open;
  var origSend = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function (method, url) {
    this.__proximo = { method: method, url: String(url) };
    return origOpen.apply(this, arguments);
  };
  XMLHttpRequest.prototype.send = function () {
    var self = this;
    var info = self.__proximo;
    if (info && info.url.indexOf("/.proximo/") === -1) {
      self.addEventListener("loadend", function () {
        crumb("xhr", self.status >= 400 || self.status === 0 ? "error" : "info",
          info.method + " " + info.url + " → " + (self.status || "failed"));
      });
    }
    return origSend.apply(this, arguments);
  };

  document.addEventListener("click", function (ev) {
    var el = ev.target;
    if (!el || !el.tagName) return;
    var label = el.tagName.toLowerCase();
    if (el.id) label += "#" + el.id;
    else if (el.className && typeof el.className === "string") label += "." + el.className.split(/\s+/)[0];
    crumb("ui", "info", "click " + label);
  }, true);

  ["pushState", "replaceState"].forEach(function (name) {
    var original = history[name];
    if (typeof original !== "function") return;
    history[name] = function (state, title, url) {
      if (url) crumb("navigation", "info", name + " → " + url);
      return original.apply(history, arguments);
    };
  });
  window.addEventListener("popstate", function () {
    crumb("navigation", "info", "popstate → " + location.pathname);
  });
})();
