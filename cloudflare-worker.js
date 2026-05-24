/**
 * Cloudflare Worker for stayawa.ke
 * --------------------------------
 * One URL, two behaviours:
 *   - PowerShell / curl  ->  returns the install script (so `irm https://stayawa.ke | iex` works)
 *   - a web browser      ->  redirects to the project website
 *
 * How it decides: browsers send `Accept: text/html` and never identify as
 * PowerShell; `irm` sends `Accept: * / *` with a "WindowsPowerShell/PowerShell"
 * user-agent. Anything that isn't clearly a browser gets the script.
 *
 * Deploy: see the step-by-step in the chat / README. In short:
 *   Cloudflare dashboard -> Workers & Pages -> Create Worker -> paste this ->
 *   Deploy -> Settings -> Domains & Routes -> Add Custom Domain -> stayawa.ke
 */

const SCRIPT_URL = "https://raw.githubusercontent.com/vdiehl/stayawake/main/bootstrap.ps1";
const WEBSITE_URL = "https://vdiehl.github.io/stayawake/"; // GitHub Pages landing page

export default {
  async fetch(request) {
    const ua = (request.headers.get("user-agent") || "").toLowerCase();
    const accept = (request.headers.get("accept") || "").toLowerCase();

    const looksLikeBrowser = accept.includes("text/html") && !ua.includes("powershell");
    if (looksLikeBrowser) {
      return Response.redirect(WEBSITE_URL, 302);
    }

    // PowerShell, curl, or anything non-browser: serve the install script.
    const upstream = await fetch(SCRIPT_URL, { cf: { cacheTtl: 300 } });
    return new Response(await upstream.text(), {
      status: upstream.ok ? 200 : 502,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "cache-control": "no-store",
      },
    });
  },
};
