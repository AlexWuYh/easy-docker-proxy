/* Shared client helpers for Stats console */
(function (global) {
  const TOKEN_KEY = "edp_session_token";
  const USER_KEY = "edp_session_user";
  const THEME_KEY = "edp_theme";
  const SITE = {
    name: "easy-docker-proxy",
    author: "alex_wuyh",
    authorUrl: "https://github.com/AlexWuYh",
    repoUrl: "https://github.com/AlexWuYh/easy-docker-proxy",
    license: "MIT",
    licenseUrl: "/stats/license.html",
    year: 2026,
  };

  function getToken() {
    return (sessionStorage.getItem(TOKEN_KEY) || localStorage.getItem(TOKEN_KEY) || "").trim();
  }
  function setSession(token, user) {
    sessionStorage.setItem(TOKEN_KEY, token);
    if (user) sessionStorage.setItem(USER_KEY, JSON.stringify(user));
  }
  function clearSession() {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(USER_KEY);
    localStorage.removeItem(TOKEN_KEY);
  }
  function getUser() {
    try {
      return JSON.parse(sessionStorage.getItem(USER_KEY) || "null");
    } catch {
      return null;
    }
  }

  function getTheme() {
    const t = localStorage.getItem(THEME_KEY);
    if (t === "light" || t === "dark") return t;
    if (window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches) {
      return "light";
    }
    return "dark";
  }

  function applyTheme(theme) {
    const t = theme === "light" ? "light" : "dark";
    document.documentElement.classList.remove("theme-light", "theme-dark");
    document.documentElement.classList.add("theme-" + t);
    document.documentElement.setAttribute("data-theme", t);
    localStorage.setItem(THEME_KEY, t);
    // Notify charts / pages that may need redraw
    try {
      window.dispatchEvent(new CustomEvent("edp-theme-change", { detail: { theme: t } }));
    } catch (_) {}
    return t;
  }

  function toggleTheme() {
    return applyTheme(getTheme() === "dark" ? "light" : "dark");
  }

  // Apply ASAP (also call from pages after DOM ready)
  applyTheme(getTheme());

  async function api(path, opts = {}) {
    const headers = Object.assign({ Accept: "application/json" }, opts.headers || {});
    const token = getToken();
    if (token) headers.Authorization = "Bearer " + token;
    if (opts.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
    let res;
    try {
      res = await fetch(path, { ...opts, headers, cache: "no-store" });
    } catch (e) {
      throw new Error("网络错误: " + (e.message || "fetch failed"));
    }
    if (res.status === 401) {
      clearSession();
      if (!location.pathname.endsWith("login.html")) {
        location.replace("/stats/login.html?next=" + encodeURIComponent(location.pathname + location.search));
      }
      throw new Error("未登录或会话已过期");
    }
    const ct = res.headers.get("Content-Type") || "";
    let data = null;
    if (ct.includes("application/json")) {
      try {
        data = await res.json();
      } catch (_) {
        data = null;
      }
    } else {
      data = await res.text();
    }
    if (!res.ok) {
      const msg = (data && data.error) || res.statusText || "request failed";
      throw new Error(msg);
    }
    return data;
  }

  function formatBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    const u = ["KB", "MB", "GB", "TB"];
    let i = -1;
    do {
      n /= 1024;
      i++;
    } while (n >= 1024 && i < u.length - 1);
    return n.toFixed(n >= 10 || i === 0 ? 1 : 2) + " " + u[i];
  }

  function formatPct(x) {
    return ((Number(x) || 0) * 100).toFixed(1) + "%";
  }

  function fmtTime(ts) {
    if (!ts) return "—";
    try {
      return new Date(ts * 1000).toLocaleString();
    } catch {
      return String(ts);
    }
  }

  function esc(s) {
    return String(s ?? "").replace(/[&<>"']/g, (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
    );
  }

  function requireAuth() {
    if (!getToken()) {
      location.replace("/stats/login.html?next=" + encodeURIComponent(location.pathname + location.search));
      return false;
    }
    return true;
  }

  function ensureFavicon() {
    if (document.querySelector('link[rel="icon"]')) return;
    const link = document.createElement("link");
    link.rel = "icon";
    link.type = "image/svg+xml";
    link.href = "/stats/favicon.svg";
    document.head.appendChild(link);
    const apple = document.createElement("link");
    apple.rel = "apple-touch-icon";
    apple.href = "/stats/favicon.svg";
    document.head.appendChild(apple);
  }

  function themeToggleHTML() {
    return `<button type="button" class="theme-toggle" id="btn-theme" title="切换深色/浅色主题" aria-label="切换主题">
      <svg class="icon-moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="M21 14.5A8.5 8.5 0 1 1 9.5 3 7 7 0 0 0 21 14.5z"/>
      </svg>
      <svg class="icon-sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="4"/>
        <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>
      </svg>
    </button>`;
  }

  function githubLinkHTML() {
    return `<a class="icon-btn" href="${SITE.repoUrl}" target="_blank" rel="noopener noreferrer" title="GitHub 仓库" aria-label="在 GitHub 打开仓库">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.3 3.44 9.8 8.21 11.39.6.11.82-.26.82-.58 0-.28-.01-1.02-.02-2-3.34.73-4.04-1.61-4.04-1.61-.55-1.39-1.33-1.76-1.33-1.76-1.09-.74.08-.73.08-.73 1.2.08 1.84 1.24 1.84 1.24 1.07 1.83 2.8 1.3 3.49 1 .11-.78.42-1.3.76-1.6-2.66-.3-5.46-1.33-5.46-5.93 0-1.31.47-2.38 1.24-3.22-.12-.3-.54-1.52.12-3.18 0 0 1.01-.32 3.3 1.23a11.5 11.5 0 0 1 6 0c2.3-1.55 3.3-1.23 3.3-1.23.66 1.66.24 2.88.12 3.18a4.65 4.65 0 0 1 1.24 3.22c0 4.61-2.8 5.62-5.48 5.92.43.37.81 1.1.81 2.22 0 1.6-.01 2.89-.01 3.28 0 .32.22.7.83.58A12.01 12.01 0 0 0 24 12c0-6.63-5.37-12-12-12z"/></svg>
    </a>`;
  }

  function wireThemeButton() {
    const btn = document.getElementById("btn-theme");
    if (!btn || btn.dataset.wired) return;
    btn.dataset.wired = "1";
    btn.addEventListener("click", () => toggleTheme());
  }

  function renderFooter() {
    ensureFavicon();
    let el = document.getElementById("app-footer");
    if (!el) {
      el = document.createElement("footer");
      el.id = "app-footer";
      const shell = document.querySelector(".app-shell") || document.body;
      shell.appendChild(el);
    }
    el.className = "site-footer";
    el.setAttribute("role", "contentinfo");
    // Single compact line — avoids large empty gap from space-between on a wide bar
    el.innerHTML = `
      <div class="footer-left">
        <span>© ${SITE.year}</span>
        <a href="${SITE.authorUrl}" target="_blank" rel="noopener noreferrer">${esc(SITE.author)}</a>
        <span class="sep">·</span>
        <a href="${SITE.repoUrl}" target="_blank" rel="noopener noreferrer">${esc(SITE.name)}</a>
        <span class="sep">·</span>
        <a href="${SITE.licenseUrl}">${SITE.license} License</a>
      </div>`;
  }

  function renderNav(active) {
    ensureFavicon();
    const user = getUser();
    const name = user ? user.username : "user";
    const role = user ? user.role : "";
    const el = document.getElementById("app-nav");
    if (!el) return;
    el.innerHTML = `
      <a class="brand" href="/stats/index.html">
        <span class="brand-mark" aria-hidden="true"></span>
        <span>EDP Console</span>
      </a>
      <nav class="nav-links" aria-label="主导航">
        <a href="/stats/index.html" class="${active === "dashboard" ? "active" : ""}">分析看板</a>
        <a href="/stats/images.html" class="${active === "images" ? "active" : ""}">镜像列表</a>
        <a href="/stats/events.html" class="${active === "events" ? "active" : ""}">事件</a>
        <a href="/stats/accounts.html" class="${active === "accounts" ? "active" : ""}">账号</a>
      </nav>
      <div class="nav-right">
        ${themeToggleHTML()}
        ${githubLinkHTML()}
        <span class="user-chip" title="当前用户">${esc(name)}${role ? " · " + esc(role) : ""}</span>
        <button type="button" class="btn btn-ghost" id="btn-logout">退出</button>
      </div>`;
    wireThemeButton();
    renderFooter();
    const btn = document.getElementById("btn-logout");
    if (btn) {
      btn.addEventListener("click", async () => {
        try {
          await api("/api/v1/auth/logout", { method: "POST" });
        } catch (_) {}
        clearSession();
        location.href = "/stats/login.html";
      });
    }
  }

  function chartColors() {
    const light = getTheme() === "light";
    return {
      blue: light ? "#2563eb" : "#3b82f6",
      blueFill: light ? "rgba(37,99,235,0.12)" : "rgba(59,130,246,0.15)",
      violet: light ? "#7c3aed" : "#a78bfa",
      violetFill: light ? "rgba(124,58,237,0.1)" : "rgba(167,139,250,0.12)",
      amber: light ? "#ea580c" : "#f97316",
      green: light ? "#059669" : "#34d399",
      red: light ? "#dc2626" : "#f87171",
      grid: light ? "rgba(100,116,139,0.18)" : "rgba(148,163,184,0.15)",
      tick: light ? "#64748b" : "#94a3b8",
      legend: light ? "#0f172a" : "#e2e8f0",
      palette: light
        ? ["#2563eb", "#ea580c", "#7c3aed", "#059669", "#dc2626", "#0284c7", "#d97706", "#9333ea"]
        : ["#3b82f6", "#f97316", "#a78bfa", "#34d399", "#f87171", "#38bdf8", "#fbbf24", "#c084fc"],
    };
  }

  global.EDP = {
    api,
    getToken,
    setSession,
    clearSession,
    getUser,
    requireAuth,
    renderNav,
    renderFooter,
    ensureFavicon,
    formatBytes,
    formatPct,
    fmtTime,
    esc,
    chartColors,
    getTheme,
    applyTheme,
    toggleTheme,
    themeToggleHTML,
    githubLinkHTML,
    wireThemeButton,
    SITE,
  };
})(window);
