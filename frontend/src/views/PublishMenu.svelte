<script lang="ts">
  import { tick } from "svelte";
  import { app, publishPullRequest } from "../stores/app.svelte";
  import PrLink from "./PrLink.svelte";

  /**
   * The control that turns a PRD's commits into a pull request.
   *
   * It is absent, not disabled, wherever publishing cannot work — a project that
   * is not a git repository, git mode off, a PRD with nothing committed. A
   * disabled control invites the user to work out what would enable it; an
   * absent one says the same thing without the puzzle. The engine decides which,
   * because the reasons are all things only it can see.
   *
   * Draft or not is a menu item rather than a checkbox beside a button: it is a
   * statement about whether this work is ready for review, which is the decision
   * being made, not a modifier on a different one.
   */

  let open = $state(false);
  let menuEl = $state<HTMLDivElement | null>(null);
  let buttonEl = $state<HTMLButtonElement | null>(null);

  const label = $derived(app.publishing ? "Publishing…" : "Pull request");

  function close(): void {
    open = false;
  }

  async function toggle(): Promise<void> {
    open = !open;
    if (!open) return;
    await tick();
    menuEl?.querySelector<HTMLButtonElement>("[role='menuitem']")?.focus();
  }

  async function choose(draft: boolean): Promise<void> {
    close();
    buttonEl?.focus();
    await publishPullRequest(draft);
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      close();
      buttonEl?.focus();
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const items = [...(menuEl?.querySelectorAll<HTMLButtonElement>("[role='menuitem']") ?? [])];
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const delta = event.key === "ArrowDown" ? 1 : -1;
    items[(at + delta + items.length) % items.length]?.focus();
  }

  // A press outside dismisses the menu, but a press on the button itself is its
  // own toggle and must not be handled twice.
  function onWindowPointerdown(event: PointerEvent): void {
    if (!open) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest(".publish")) return;
    close();
  }
</script>

<svelte:window onpointerdown={onWindowPointerdown} />

{#if app.canPublish || app.publishing}
  <span class="publish">
    <button
      bind:this={buttonEl}
      aria-haspopup="menu"
      aria-expanded={open}
      disabled={app.publishing}
      onclick={toggle}
    >
      {label}
    </button>

    {#if open}
      <div
        bind:this={menuEl}
        class="menu"
        role="menu"
        tabindex="-1"
        aria-label="Publish this PRD"
        onkeydown={onKeydown}
      >
        <button role="menuitem" onclick={() => choose(false)}>Create pull request</button>
        <button role="menuitem" onclick={() => choose(true)}>Create draft pull request</button>
      </div>
    {/if}
  </span>
{/if}

<!-- The result of the last publish, so the pull request that was just opened is
     reachable without waiting for a GitHub refresh to confirm it. -->
{#if app.published?.pr}
  <PrLink pr={app.published.pr} now={app.now} />
{/if}

<style>
  .publish {
    position: relative;
    display: inline-flex;
  }

  /* The toolbar's button styling is scoped to App.svelte, so it is restated
     here rather than inherited. */
  .publish > button {
    padding: 3px 10px;
    background: var(--bg-raised);
    color: var(--fg-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    cursor: default;
  }
  .publish > button:hover:not(:disabled) {
    color: var(--fg-1);
    border-color: var(--fg-3);
  }
  .publish > button:disabled {
    opacity: 0.4;
  }

  .menu {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 20;
    display: flex;
    flex-direction: column;
    min-width: 200px;
    padding: 4px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    box-shadow: 0 6px 20px rgb(0 0 0 / 35%);
  }

  .menu button {
    border: 0;
    background: none;
    border-radius: 3px;
    text-align: left;
    padding: 5px 8px;
    color: var(--fg-2);
  }
  .menu button:hover,
  .menu button:focus-visible {
    background: var(--bg);
    color: var(--fg-1);
  }
</style>
