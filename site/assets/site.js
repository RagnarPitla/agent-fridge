// SPDX-License-Identifier: Apache-2.0

(() => {
  const root = document.documentElement;
  const themeToggle = document.querySelector("[data-theme-toggle]");
  const themeLabel = themeToggle?.querySelector(".toggle-label");
  const themeQuery = () => new URL(window.location.href).searchParams.get("clawpilotTheme");

  function updateThemeLinks() {
    const value = themeQuery();
    document.querySelectorAll("a[data-preserve-theme]").forEach((link) => {
      const target = new URL(link.getAttribute("href"), window.location.href);
      if (value) target.searchParams.set("clawpilotTheme", value);
      else target.searchParams.delete("clawpilotTheme");
      link.setAttribute("href", `${target.pathname}${target.search}${target.hash}`);
    });
  }

  function syncThemeControl() {
    if (!themeToggle) return;
    const theme = root.getAttribute("data-theme") === "dark" ? "dark" : "light";
    const next = theme === "dark" ? "light" : "dark";
    themeToggle.setAttribute("aria-label", `Switch to ${next} theme`);
    themeToggle.setAttribute("aria-pressed", theme === "dark" ? "true" : "false");
    if (themeLabel) themeLabel.textContent = theme === "dark" ? "Dark" : "Light";
  }

  function setTheme(theme, updateUrl) {
    const value = theme === "dark" ? "dark" : "light";
    root.setAttribute("data-theme", value);
    if (updateUrl) {
      const url = new URL(window.location.href);
      url.searchParams.set("clawpilotTheme", value);
      window.history.replaceState({}, "", `${url.pathname}${url.search}${url.hash}`);
    }
    syncThemeControl();
    updateThemeLinks();
  }

  themeToggle?.addEventListener("click", () => {
    const current = root.getAttribute("data-theme") === "dark" ? "dark" : "light";
    setTheme(current === "dark" ? "light" : "dark", true);
  });

  syncThemeControl();
  updateThemeLinks();

  const progressBar = document.querySelector("[data-reading-progress]");
  let progressFrame = 0;

  function updateProgress() {
    progressFrame = 0;
    if (!progressBar) return;
    const available = document.documentElement.scrollHeight - window.innerHeight;
    const progress = available > 0 ? Math.min(1, Math.max(0, window.scrollY / available)) : 0;
    progressBar.style.transform = `scaleX(${progress})`;
  }

  window.addEventListener("scroll", () => {
    if (!progressFrame) progressFrame = window.requestAnimationFrame(updateProgress);
  }, { passive: true });
  window.addEventListener("resize", updateProgress);
  updateProgress();

  const navLinks = [...document.querySelectorAll(".section-nav a[href^='#']")];
  const sectionById = new Map(navLinks.map((link) => [
    link.getAttribute("href").slice(1),
    link,
  ]));

  if ("IntersectionObserver" in window && sectionById.size) {
    const visible = new Map();
    const observer = new IntersectionObserver((entries) => {
      entries.forEach((entry) => {
        if (entry.isIntersecting) visible.set(entry.target.id, entry.boundingClientRect.top);
        else visible.delete(entry.target.id);
      });
      const active = [...visible.entries()].sort((a, b) => Math.abs(a[1]) - Math.abs(b[1]))[0]?.[0];
      navLinks.forEach((link) => link.removeAttribute("aria-current"));
      if (active) sectionById.get(active)?.setAttribute("aria-current", "location");
    }, {
      rootMargin: "-22% 0px -62% 0px",
      threshold: [0, 0.1, 0.5],
    });
    sectionById.forEach((_, id) => {
      const section = document.getElementById(id);
      if (section) observer.observe(section);
    });
  }

  const copyStatus = document.querySelector("[data-copy-status]");

  function fallbackCopy(text) {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.append(area);
    area.select();
    const copied = document.execCommand("copy");
    area.remove();
    return copied;
  }

  async function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
    return fallbackCopy(text);
  }

  document.querySelectorAll("[data-copy-target]").forEach((button) => {
    button.addEventListener("click", async () => {
      const target = document.getElementById(button.dataset.copyTarget);
      if (!target) return;
      const original = button.textContent;
      try {
        const copied = await copyText(target.textContent.trim());
        button.textContent = copied ? "Copied" : "Select text";
        if (copyStatus) copyStatus.textContent = copied ? "Command copied to clipboard." : "Copy was unavailable. Select the command manually.";
      } catch {
        button.textContent = "Select text";
        if (copyStatus) copyStatus.textContent = "Copy was unavailable. Select the command manually.";
      }
      window.setTimeout(() => {
        button.textContent = original;
      }, 1600);
    });
  });

  const tabs = [...document.querySelectorAll("[role='tab'][data-pane]")];
  const panels = [...document.querySelectorAll("[role='tabpanel'][data-pane-panel]")];
  const claimCards = [...document.querySelectorAll("[data-lane]")];

  function activatePane(name, focus) {
    tabs.forEach((tab) => {
      const selected = tab.dataset.pane === name;
      tab.setAttribute("aria-selected", selected ? "true" : "false");
      tab.tabIndex = selected ? 0 : -1;
      if (selected && focus) tab.focus();
    });
    panels.forEach((panel) => {
      panel.hidden = panel.dataset.panePanel !== name;
    });
    claimCards.forEach((card) => {
      card.dataset.active = card.dataset.lane === name ? "true" : "false";
    });
  }

  tabs.forEach((tab, index) => {
    tab.addEventListener("click", () => activatePane(tab.dataset.pane, false));
    tab.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      let next = index;
      if (event.key === "ArrowLeft") next = (index - 1 + tabs.length) % tabs.length;
      if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
      if (event.key === "Home") next = 0;
      if (event.key === "End") next = tabs.length - 1;
      activatePane(tabs[next].dataset.pane, true);
    });
  });

  if (tabs.length) activatePane(tabs.find((tab) => tab.getAttribute("aria-selected") === "true")?.dataset.pane || tabs[0].dataset.pane, false);

  const collisionButton = document.querySelector("[data-trigger-collision]");
  const secondClaim = document.querySelector("[data-second-claim]");
  const secondResult = document.querySelector("[data-second-result]");
  const secondExit = document.querySelector("[data-second-exit]");
  const blockedClaim = document.querySelector("[data-blocked-claim]");
  const collisionStatus = document.querySelector("[data-collision-status]");
  let collisionActive = false;

  collisionButton?.addEventListener("click", () => {
    collisionActive = !collisionActive;
    if (secondClaim) {
      secondClaim.textContent = collisionActive
        ? '$ fridge claim "design/checkout/copy.md" --task "Patch UX copy"'
        : '$ fridge claim "src/api/checkout/**" --task "Build checkout API for PR #42"';
    }
    if (secondResult) {
      secondResult.textContent = collisionActive
        ? "E_CONFLICT: design-copilot already holds design/checkout/**"
        : "Card clm_api is yours.";
      secondResult.className = collisionActive ? "exit-conflict" : "exit-ok";
    }
    if (secondExit) {
      secondExit.textContent = collisionActive ? "exit 10 - no write started" : "exit 0";
      secondExit.className = collisionActive ? "exit-conflict" : "exit-ok";
    }
    if (blockedClaim) blockedClaim.hidden = !collisionActive;
    collisionButton.textContent = collisionActive ? "Reset scene" : "Trigger collision";
    collisionButton.setAttribute("aria-pressed", collisionActive ? "true" : "false");
    if (collisionStatus) {
      collisionStatus.textContent = collisionActive
        ? "Collision triggered. The second claim was refused with exit 10 and the board now shows the blocked attempt."
        : "Collision reset. The four original claims are disjoint.";
    }
    if (collisionActive) activatePane("codex", false);
  });
})();
