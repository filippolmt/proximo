// Captures a real Sentry envelope from the vendored agent, so the parser is
// tested against what the SDK actually sends rather than against a hand-written
// approximation. Run via `make capture-envelope`.
const http = require("http");
const fs = require("fs");
const puppeteer = require("puppeteer");

const agent =
  fs.readFileSync("internal/inspect/assets/sentry.min.js", "utf8") + "\n" +
  fs.readFileSync("internal/inspect/assets/agent.js", "utf8");

const page = `<html><head><title>capture</title>
<script src="/.proximo/agent.js" data-proximo-exchange="cafebabe00000001"></script>
</head><body><p>captured</p></body></html>`;

let captured = null;
const srv = http.createServer((req, res) => {
  if (req.url.startsWith("/.proximo/agent.js")) {
    res.writeHead(200, { "Content-Type": "application/javascript" });
    return res.end(agent);
  }
  if (req.url.startsWith("/.proximo/ingest")) {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks);
      // Keep the first envelope that carries an exception; the SDK also sends
      // sessions, which are not what the parser is for.
      if (!captured && body.includes("exception")) captured = body;
      res.writeHead(200).end();
    });
    return;
  }
  res.writeHead(200, { "Content-Type": "text/html" }).end(page);
});

(async () => {
  await new Promise((r) => srv.listen(8099, "127.0.0.1", r));
  const b = await puppeteer.launch({ args: ["--no-sandbox"] });
  const p = await b.newPage();
  await p.goto("http://127.0.0.1:8099/", { waitUntil: "networkidle0" });
  await p.evaluate(() => {
    console.warn("about to break");
    setTimeout(() => { window.__missing.total; }, 10);
  });
  await new Promise((r) => setTimeout(r, 4000));
  await b.close();
  srv.close();

  if (!captured) throw new Error("no envelope with an exception was captured");
  fs.writeFileSync("internal/inspect/testdata/envelope.bin", captured);
  const banner = fs.readFileSync("internal/inspect/assets/sentry.min.js", "utf8").slice(0, 200);
  const version = /@sentry\/browser ([0-9.]+)/.exec(banner)[1];
  fs.writeFileSync("internal/inspect/testdata/envelope.version", version + "\n");
  console.log(`captured ${captured.length} bytes from @sentry/browser ${version}`);
})().catch((e) => { console.error(e.message); process.exit(1); });
