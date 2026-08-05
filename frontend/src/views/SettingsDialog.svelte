<script lang="ts">
  import { onMount, tick } from "svelte";
  import Settings from "./Settings.svelte";

  /**
   * Settings as a dialog rather than a third tab.
   *
   * Stories and New PRD are per-PRD working contexts; project configuration is
   * not one of those, and making them tab peers implied they were the same kind
   * of thing — opening Settings also hid whatever you were watching. As a dialog
   * it opens over the run, which keeps going behind it.
   *
   * Focus is trapped inside, Escape dismisses, and focus returns to whatever
   * opened it — the same contract UsagePanel keeps, so there is one modal
   * behaviour in the app rather than two.
   */
  let { onclose }: { onclose: () => void } = $props();

  let dialogEl = $state<HTMLDivElement | null>(null);
  let opener: HTMLElement | null = null;

  onMount(() => {
    // The menu accelerator can fire with focus anywhere, including on nothing.
    opener = document.activeElement as HTMLElement | null;
    void focusFirst();
    return () => opener?.focus?.();
  });

  async function focusFirst(): Promise<void> {
    await tick();
    const target =
      dialogEl?.querySelector<HTMLElement>("[data-autofocus]") ??
      dialogEl?.querySelector<HTMLElement>(FOCUSABLE) ??
      dialogEl;
    target?.focus();
  }

  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  /**
   * Escape closes; Tab cycles within the dialog so focus can never land on the
   * run behind it, which would leave the user editing something they cannot see.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      onclose();
      return;
    }
    if (event.key !== "Tab" || !dialogEl) return;

    const focusables = [...dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE)];
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];

    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
      return;
    }
    if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<!-- Decorative: dismissing by clicking outside is a shortcut, and Escape and the
     close button are the real keyboard paths. Same shape as UsagePanel's scrim. -->
<div
  class="overlay"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onclose();
  }}
>
  <div
    class="panel"
    role="dialog"
    aria-modal="true"
    aria-label="Settings"
    tabindex="-1"
    bind:this={dialogEl}
    onkeydown={onKeydown}
  >
    <header class="head">
      <h2>Settings</h2>
      <button class="close" onclick={onclose} aria-label="Close settings" data-autofocus>×</button>
    </header>

    <Settings />
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    background: rgba(0, 0, 0, 0.45);
  }

  .panel {
    display: flex;
    flex-direction: column;
    width: min(760px, 100%);
    /* Bounded so the dialog never grows past the window; the settings body
       scrolls inside it rather than the page scrolling behind. */
    max-height: min(720px, 100%);
    min-height: 0;
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: var(--radius-panel, 8px);
    box-shadow: 0 24px 64px rgba(0, 0, 0, 0.45);
    overflow: hidden;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    flex: none;
  }

  h2 {
    margin: 0;
    font-size: 14px;
    font-weight: 500;
    color: var(--fg-1);
  }

  .close {
    margin-left: auto;
    width: 26px;
    height: 26px;
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    color: var(--fg-3);
    font-size: 17px;
    line-height: 1;
    cursor: default;
  }
  .close:hover,
  .close:focus-visible {
    color: var(--fg-1);
    border-color: var(--border);
    background: var(--bg-raised);
  }
</style>
