<script lang="ts">
  import { tick } from "svelte";
  import { app, publishPullRequest, publishStack } from "../stores/app.svelte";
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
   *
   * One pull request per story is offered only where the run produced a branch
   * per story. Where it did not, the item is replaced by the reason — the whole
   * control is present and one of its items is missing, which is worth a sentence
   * rather than leaving the user to work out why the menu is shorter than
   * someone else's.
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

  async function chooseStack(draft: boolean): Promise<void> {
    close();
    buttonEl?.focus();
    await publishStack(draft);
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
        {#if app.canPublishStack}
          <button role="menuitem" onclick={() => chooseStack(false)}>
            Create stacked pull requests
          </button>
          <button role="menuitem" onclick={() => chooseStack(true)}>
            Create draft stacked pull requests
          </button>
        {:else if app.publishOffer?.stackReason}
          <p class="reason">{app.publishOffer.stackReason}</p>
        {/if}
      </div>
    {/if}
  </span>
{/if}

<!-- The result of the last publish, so the pull request that was just opened is
     reachable without waiting for a GitHub refresh to confirm it. -->
{#if app.published?.pr}
  <PrLink pr={app.published.pr} now={app.now} />
{/if}

<!-- A stack's result is a list: every story that got a pull request, shown with
     its link, and the ones that did not with the reason. -->
{#if app.publishedStack?.stories?.length}
  <ul class="stack">
    {#each app.publishedStack.stories as story (story.storyId)}
      <li>
        <span class="story">{story.storyId}</span>
        {#if story.pr}
          <PrLink pr={story.pr} now={app.now} />
        {:else}
          <span class="reason">{story.error || story.skipped}</span>
        {/if}
      </li>
    {/each}
  </ul>
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

  .reason {
    margin: 0;
    padding: 5px 8px;
    color: var(--fg-3);
    font-size: 0.9em;
  }

  /* The per-story results sit beside the control rather than inside the menu:
     the menu is dismissed the moment an item is chosen, and the outcome has to
     outlive it. */
  .stack {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 10px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .stack li {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .stack .story {
    color: var(--fg-3);
  }
</style>
