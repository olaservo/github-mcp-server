import {
  App,
  applyDocumentTheme,
  applyHostStyleVariables,
  applyHostFonts,
} from "@modelcontextprotocol/ext-apps";
import { parseDiff } from "./diff-parser";
import { renderDiff, setViewMode, getViewMode } from "./diff-renderer";
import "./styles.css";

interface DiffResult {
  diff: string;
  owner: string;
  repo: string;
  pullNumber: number;
  viewMode?: "unified" | "split";
}

let app: App | null = null;

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function handleHostContextChanged(ctx: any): void {
  if (ctx.theme) {
    applyDocumentTheme(ctx.theme);
  }
  if (ctx.styles?.variables) {
    applyHostStyleVariables(ctx.styles.variables);
  }
  if (ctx.styles?.css?.fonts) {
    applyHostFonts(ctx.styles.css.fonts);
  }

  // Apply safe area insets
  if (ctx.safeAreaInsets) {
    document.body.style.paddingTop = `${ctx.safeAreaInsets.top}px`;
    document.body.style.paddingBottom = `${ctx.safeAreaInsets.bottom}px`;
    document.body.style.paddingLeft = `${ctx.safeAreaInsets.left}px`;
    document.body.style.paddingRight = `${ctx.safeAreaInsets.right}px`;
  }

  // Update fullscreen button visibility and state
  const fullscreenBtn = document.getElementById("fullscreen-btn");
  if (fullscreenBtn) {
    // Only update visibility if availableDisplayModes is present
    if (ctx.availableDisplayModes) {
      const canFullscreen = ctx.availableDisplayModes.includes("fullscreen");
      fullscreenBtn.style.display = canFullscreen ? "flex" : "none";
    }

    // Update button state based on current display mode
    if (ctx.displayMode) {
      const isFullscreen = ctx.displayMode === "fullscreen";
      fullscreenBtn.textContent = isFullscreen ? "✕" : "⛶";
      fullscreenBtn.title = isFullscreen ? "Exit fullscreen" : "Fullscreen";
      document.body.classList.toggle("fullscreen", isFullscreen);
    }
  }
}

async function toggleFullscreen(): Promise<void> {
  if (!app) return;

  const ctx = app.getHostContext();
  const currentMode = ctx?.displayMode || "inline";
  const newMode = currentMode === "fullscreen" ? "inline" : "fullscreen";

  if (ctx?.availableDisplayModes?.includes(newMode)) {
    await app.requestDisplayMode({ mode: newMode });
  }
}

function toggleViewMode(): void {
  const currentMode = getViewMode();
  const newMode = currentMode === "unified" ? "split" : "unified";
  setViewMode(newMode);
  updateViewModeButton();
}

function updateViewModeButton(): void {
  const btn = document.getElementById("view-mode-btn");
  if (btn) {
    const mode = getViewMode();
    btn.textContent = mode === "unified" ? "Split" : "Unified";
    btn.title = mode === "unified" ? "Switch to split view" : "Switch to unified view";
  }
}

function init(): void {
  app = new App({ name: "PR Diff Viewer", version: "1.0.0" });

  // Handle tool results
  app.ontoolresult = (result) => {
    const data = result.structuredContent as DiffResult | undefined;
    if (data?.diff) {
      // Update title with PR info as clickable link
      const title = document.getElementById("title");
      if (title) {
        const prUrl = `https://github.com/${data.owner}/${data.repo}/pull/${data.pullNumber}`;
        title.innerHTML = `<span class="pr-link">${data.owner}/${data.repo} #${data.pullNumber}</span>`;
        const link = title.querySelector(".pr-link");
        if (link) {
          link.addEventListener("click", () => {
            app?.openLink({ url: prUrl });
          });
        }
      }

      // Apply view mode from tool parameters
      if (data.viewMode) {
        setViewMode(data.viewMode);
        updateViewModeButton();
      }

      // Parse and render diff
      const parsed = parseDiff(data.diff);
      renderDiff(parsed);
    }
  };

  // Handle streaming partial input (for progressive rendering)
  app.ontoolinputpartial = (input) => {
    const data = input as Partial<DiffResult> | undefined;
    if (data?.diff) {
      const parsed = parseDiff(data.diff);
      renderDiff(parsed);
    }
  };

  // Handle host context changes (theme, etc.)
  app.onhostcontextchanged = handleHostContextChanged;

  // Set up view mode toggle button
  const viewModeBtn = document.getElementById("view-mode-btn");
  if (viewModeBtn) {
    viewModeBtn.addEventListener("click", toggleViewMode);
  }

  // Set up fullscreen button
  const fullscreenBtn = document.getElementById("fullscreen-btn");
  if (fullscreenBtn) {
    fullscreenBtn.addEventListener("click", toggleFullscreen);
  }

  // Connect to host
  app.connect().then(() => {
    const ctx = app?.getHostContext();
    if (ctx) {
      handleHostContextChanged(ctx);
    }
  });
}

// Initialize when DOM is ready
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
