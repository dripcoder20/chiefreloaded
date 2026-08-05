<script lang="ts">
  import { tick } from "svelte";
  import {
    app,
    selectPrd,
    requestNewPRD,
    editPrd,
    openPrdFile,
    confirmDeletePrd,
    openOnGitHub,
    openInApp,
  } from "../stores/app.svelte";

  /**
   * The PRD list is navigation, not a dialog.
   *
   * chief hides it behind a modal you open with `l`, which occludes everything —
   * yet it shows live per-PRD state with a ticking iteration counter, i.e. it is
   * something you are meant to watch. Those two facts contradict each other in a
   * terminal and do not have to here, so it becomes a permanent rail. It also
   * absorbs chief's tab bar, which renders the same list a second time.
   *
   * Above the list sits a New PRD action and a divider, and every row carries a
   * three-dot overflow button plus a right-click context menu. Both menus offer
   * the same three actions and act on the PRD whose row opened them, regardless
   * of which PRD happens to be selected.
   */

  /**
   * The state to show for a PRD.
   *
   * A live run in this session wins — it is the only thing that can report
   * running, paused or failed. Otherwise the PRD's own state stands, which is
   * what the document says: reopening a project full of finished PRDs used to
   * show every one of them as untouched, because this only ever looked at runs
   * and there are none in a fresh session.
   */
  function stateOf(prd: { name: string; state?: string }): string {
    return app.runs.find((r) => r.prd === prd.name)?.state ?? prd.state ?? "idle";
  }

  // The three actions, shared verbatim by the dropdown and the context menu so
  // there is exactly one definition of order, labels and behaviour.
  type Action = {
    key: string;
    label: string;
    run: (prd: string) => void | Promise<void>;
    destructive?: boolean;
  };

  const ACTIONS: Action[] = [
    { key: "edit", label: "Edit PRD", run: (p) => editPrd(p) },
    { key: "open", label: "Open markdown file", run: (p) => openPrdFile(p) },
    // Deleting asks first; the dialog names its target before any file is
    // removed. See the confirmation in App.svelte.
    { key: "delete", label: "Delete PRD", run: (p) => confirmDeletePrd(p), destructive: true },
  ];

  // A single open-menu state means opening one menu necessarily closes any other.
  // `trigger` distinguishes the three-dot dropdown (anchored to its button) from
  // the context menu (anchored at the cursor); `x`/`y` are viewport coordinates.
  type OpenMenu = {
    prd: string;
    x: number;
    y: number;
    trigger: "button" | "context";
  };

  let menu = $state<OpenMenu | null>(null);
  let menuEl = $state<HTMLDivElement | null>(null);

  function isButtonMenuOpen(name: string): boolean {
    return menu?.prd === name && menu.trigger === "button";
  }

  function closeMenu(): void {
    menu = null;
  }

  async function openFromButton(event: MouseEvent, name: string): Promise<void> {
    event.stopPropagation();
    // A second click on the same button is a toggle.
    if (isButtonMenuOpen(name)) {
      closeMenu();
      return;
    }
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    menu = { prd: name, x: rect.right, y: rect.bottom + 2, trigger: "button" };
    await focusFirstItem();
  }

  async function openFromContext(event: MouseEvent, name: string): Promise<void> {
    event.preventDefault();
    menu = { prd: name, x: event.clientX, y: event.clientY, trigger: "context" };
    await focusFirstItem();
  }

  async function focusFirstItem(): Promise<void> {
    await tick();
    const first = menuEl?.querySelector<HTMLButtonElement>("[role='menuitem']");
    first?.focus();
  }

  async function activate(action: Action): Promise<void> {
    const prd = menu?.prd;
    if (!prd) return;
    // Completing an action dismisses the menu.
    closeMenu();
    await action.run(prd);
  }

  // Roving focus inside the menu, plus Escape to dismiss.
  function onMenuKeydown(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      closeMenu();
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const items = [...(menuEl?.querySelectorAll<HTMLButtonElement>("[role='menuitem']") ?? [])];
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const delta = event.key === "ArrowDown" ? 1 : -1;
    const next = (at + delta + items.length) % items.length;
    items[next]?.focus();
  }

  // Focus leaving the menu dismisses it — but only when focus lands outside;
  // moving between items keeps it open.
  function onMenuFocusout(event: FocusEvent): void {
    const next = event.relatedTarget as Node | null;
    if (next && menuEl?.contains(next)) return;
    closeMenu();
  }

  // Outside clicks dismiss. Pointerdown fires before click, and this runs while a
  // menu is open: ignore presses on a three-dot button (its own click toggles or
  // re-targets) and inside the menu (its items handle themselves); everything
  // else is "outside".
  function onWindowPointerdown(event: PointerEvent): void {
    if (!menu) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest(".prd-menu")) return;
    if (target?.closest(".dots")) return;
    closeMenu();
  }

  function onWindowKeydown(event: KeyboardEvent): void {
    if (menu && event.key === "Escape") closeMenu();
    if (ideOpen && event.key === "Escape") closeIde();
  }

  // --- repository launchers -------------------------------------------------

  /**
   * Every AI IDE is listed whether or not it is installed. Hiding an entry would
   * leave a user who expected it there with no control to activate and therefore
   * no explanation; selecting an unavailable one produces the alert that names
   * what to install.
   */
  const IDES = [
    { app: "claude", label: "Claude" },
    { app: "cursor", label: "Cursor" },
    { app: "codex", label: "Codex" },
  ];

  let ideOpen = $state(false);
  let ideMenuEl = $state<HTMLDivElement | null>(null);
  let ideButtonEl = $state<HTMLButtonElement | null>(null);

  function statusOf(target: string): string {
    const found = app.localApps.find((a) => a.app === target);
    if (!found) return "";
    return found.available ? "" : "not installed";
  }

  function closeIde(): void {
    ideOpen = false;
  }

  async function toggleIde(): Promise<void> {
    ideOpen = !ideOpen;
    if (!ideOpen) return;
    await tick();
    ideMenuEl?.querySelector<HTMLButtonElement>("[role='menuitem']")?.focus();
  }

  async function launchIde(target: string): Promise<void> {
    closeIde();
    ideButtonEl?.focus();
    await openInApp(target);
  }

  function onIdeKeydown(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      closeIde();
      ideButtonEl?.focus();
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const items = [...(ideMenuEl?.querySelectorAll<HTMLButtonElement>("[role='menuitem']") ?? [])];
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const delta = event.key === "ArrowDown" ? 1 : -1;
    items[(at + delta + items.length) % items.length]?.focus();
  }
</script>

<svelte:window onpointerdown={onWindowPointerdown} onkeydown={onWindowKeydown} />

<nav class="rail" aria-label="PRDs">
  <div class="head">PRDS</div>

  <button class="new" onclick={requestNewPRD}>
    <span class="plus" aria-hidden="true">+</span>
    New PRD
  </button>

  <!-- Repository launchers sit with New PRD, above the divider, so the divider
       still separates "things you do to the project" from the PRD list. -->
  <div class="launchers">
    <button class="launch" title="Open GitHub repository" aria-label="Open GitHub repository" onclick={openOnGitHub}>
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" fill="currentColor">
        <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
      </svg>
    </button>

    <button class="launch" title="Open in VS Code" aria-label="Open in VS Code" onclick={() => openInApp("vscode")}>
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" fill="currentColor">
        <path d="M11.6.29 6.4 5.13 3.2 2.7 2 3.24v9.52l1.2.54 3.2-2.43 5.2 4.84L15 14.5v-13L11.6.29ZM3.5 10.6V5.4L6 8l-2.5 2.6Zm8.2 1.7L7.9 8l3.8-4.3v8.6Z" />
      </svg>
    </button>

    <!-- A neutral icon: the dropdown must not imply one of the three is chosen. -->
    <button
      bind:this={ideButtonEl}
      class="launch"
      title="Open in AI IDE"
      aria-label="Open in AI IDE"
      aria-haspopup="menu"
      aria-expanded={ideOpen}
      onclick={toggleIde}
    >
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" fill="currentColor">
        <path d="M8 1.2 9.5 5 13.3 6.5 9.5 8 8 11.8 6.5 8 2.7 6.5 6.5 5 8 1.2ZM12.7 10.2l.6 1.5 1.5.6-1.5.6-.6 1.5-.6-1.5-1.5-.6 1.5-.6.6-1.5Z" />
      </svg>
    </button>

    {#if ideOpen}
      <div
        bind:this={ideMenuEl}
        class="ide-menu"
        role="menu"
        tabindex="-1"
        aria-label="Open the repository in an AI IDE"
        onkeydown={onIdeKeydown}
      >
        {#each IDES as ide (ide.app)}
          {@const note = statusOf(ide.app)}
          <button role="menuitem" onclick={() => launchIde(ide.app)}>
            <span>{ide.label}</span>
            {#if note}<span class="note">{note}</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <div class="divider" role="separator"></div>

  {#if app.prds.length === 0}
    <p class="empty">No PRDs found.</p>
  {/if}

  {#each app.prds as prd (prd.name)}
    {@const state = stateOf(prd)}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <!-- The context menu duplicates the keyboard-accessible three-dot button, so
         the right-click shortcut on this layout row is a pure enhancement. -->
    <div class="row" class:current={app.selectedPrd === prd.name} oncontextmenu={(e) => openFromContext(e, prd.name)}>
      <button class="select" onclick={() => selectPrd(prd.name)}>
        <span class="line">
          <span class="dot {state}" class:pulse={state === "running"}></span>
          <span class="name">{prd.name}</span>
          <span class="count tnum">{prd.completed}/{prd.total}</span>
        </span>

        <span class="meter" aria-hidden="true">
          <span
            class="fill {state}"
            style="width: {prd.total ? (prd.completed / prd.total) * 100 : 0}%"
          ></span>
        </span>

        {#if prd.parseError}
          <span class="warn">unreadable</span>
        {:else if prd.legacy}
          <span class="warn">legacy layout</span>
        {:else if prd.branch}
          <span class="branch">⎇ {prd.branch}</span>
        {/if}
      </button>

      <button
        class="dots"
        aria-label="Actions for {prd.name}"
        aria-haspopup="menu"
        aria-expanded={isButtonMenuOpen(prd.name)}
        onclick={(e) => openFromButton(e, prd.name)}
      >
        <span aria-hidden="true">⋯</span>
      </button>
    </div>
  {/each}
</nav>

{#if menu}
  <div
    bind:this={menuEl}
    class="prd-menu"
    class:end={menu.trigger === "button"}
    role="menu"
    tabindex="-1"
    aria-label="Actions for {menu.prd}"
    style="left: {menu.x}px; top: {menu.y}px;"
    onkeydown={onMenuKeydown}
    onfocusout={onMenuFocusout}
  >
    {#each ACTIONS as action (action.key)}
      <button
        role="menuitem"
        class:destructive={action.destructive}
        onclick={() => activate(action)}
      >
        {action.label}
      </button>
    {/each}
  </div>
{/if}

<style>
  .rail {
    display: flex;
    flex-direction: column;
    width: 236px;
    flex: none;
    border-right: 1px solid var(--border);
    background: var(--bg-panel);
    overflow-y: auto;
  }

  .head {
    padding: 12px 14px 6px;
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.06em;
    color: var(--fg-3);
  }

  .new {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 8px 6px;
    padding: 6px 10px;
    background: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--fg-2);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .new:hover {
    color: var(--fg-1);
    border-color: var(--fg-3);
  }
  .plus {
    font-size: 14px;
    line-height: 1;
    color: var(--fg-3);
  }

  /* A compact group beside New PRD, which stays the primary sidebar action. */
  .launchers {
    position: relative;
    display: flex;
    gap: 2px;
    margin: 0 8px 8px;
  }

  .launch {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 24px;
    padding: 0;
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    color: var(--fg-3);
    cursor: default;
  }
  .launch:hover,
  .launch:focus-visible,
  .launch[aria-expanded="true"] {
    color: var(--fg-1);
    border-color: var(--border);
    background: var(--bg-raised);
  }

  .ide-menu {
    position: absolute;
    z-index: 40;
    top: calc(100% + 2px);
    left: 0;
    display: flex;
    flex-direction: column;
    min-width: 160px;
    padding: 4px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
  }
  .ide-menu button {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 6px 10px;
    background: none;
    border: 0;
    border-radius: var(--radius-control);
    color: var(--fg-2);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .ide-menu button:hover,
  .ide-menu button:focus-visible {
    background: var(--bg-app);
    color: var(--fg-1);
  }
  /* Installation status is secondary text only; the entry stays selectable so
     activating it can raise the alert that names what to install. */
  .note {
    font-size: 10.5px;
    color: var(--fg-3);
  }

  .divider {
    height: 1px;
    margin: 0 0 4px;
    background: var(--border);
  }

  .empty {
    padding: 4px 14px;
    color: var(--fg-3);
    margin: 0;
  }

  .row {
    display: flex;
    align-items: stretch;
    border-left: 2px solid transparent;
  }
  .row:hover {
    background: var(--bg-raised);
  }
  .row.current {
    background: var(--bg-raised);
    border-left-color: var(--accent);
  }

  .select {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: 1;
    min-width: 0;
    padding: 7px 4px 7px 12px;
    background: none;
    border: 0;
    color: var(--fg-2);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .row.current .select {
    color: var(--fg-1);
  }

  .line {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .count {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  /* The three-dot button is invisible until the row is hovered or anything in it
     is focused; it also stays lit while its own menu is open. On touch devices,
     where there is no hover, it is always shown. */
  .dots {
    flex: none;
    align-self: center;
    width: 24px;
    margin-right: 6px;
    padding: 2px 0;
    background: none;
    border: 0;
    border-radius: var(--radius-control);
    color: var(--fg-3);
    font-size: 15px;
    line-height: 1;
    cursor: default;
    opacity: 0;
  }
  .row:hover .dots,
  .row:focus-within .dots,
  .dots[aria-expanded="true"] {
    opacity: 1;
  }
  .dots:hover,
  .dots:focus-visible {
    color: var(--fg-1);
    background: var(--bg-app);
  }
  @media (hover: none) {
    .dots {
      opacity: 1;
    }
  }

  /* A filled dot for idle, a pulsing one for running: motion means work is
     happening, and it is the peripheral-vision signal that something is live. */
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--fg-3);
    flex: none;
  }
  .dot.running {
    background: var(--accent);
  }
  .dot.complete {
    background: var(--ok);
  }
  .dot.paused {
    background: var(--warn);
  }
  .dot.error {
    background: var(--danger);
  }

  .pulse {
    animation: pulse 1.6s ease-in-out infinite;
  }

  @keyframes pulse {
    0%,
    100% {
      opacity: 0.4;
    }
    50% {
      opacity: 1;
    }
  }

  .meter {
    display: block;
    height: 3px;
    background: var(--n-3);
    border-radius: 2px;
    overflow: hidden;
  }

  .fill {
    display: block;
    height: 100%;
    background: var(--fg-3);
  }
  .fill.running {
    background: var(--accent);
  }
  .fill.complete {
    background: var(--ok);
  }

  .branch,
  .warn {
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--fg-3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .warn {
    color: var(--warn);
  }

  .prd-menu {
    position: fixed;
    z-index: 50;
    display: flex;
    flex-direction: column;
    min-width: 172px;
    padding: 4px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
  }
  /* A button-anchored dropdown is right-aligned to its trigger so it never spills
     off the narrow rail; the context menu opens from the cursor as-is. */
  .prd-menu.end {
    transform: translateX(-100%);
  }

  .prd-menu button {
    padding: 6px 10px;
    background: none;
    border: 0;
    border-radius: var(--radius-control);
    color: var(--fg-2);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .prd-menu button:hover,
  .prd-menu button:focus-visible {
    background: var(--bg-app);
    color: var(--fg-1);
  }
  .prd-menu button.destructive {
    color: var(--danger);
  }

  @media (prefers-reduced-motion: reduce) {
    /* Keep the distinction, drop the animation. */
    .pulse {
      animation: none;
      opacity: 1;
    }
  }
</style>
