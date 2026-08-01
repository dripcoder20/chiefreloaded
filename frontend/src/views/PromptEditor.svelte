<script lang="ts">
  import { api, type Prompt } from "../platform";

  /**
   * The brief handed to the agent when it writes a PRD.
   *
   * chief fixes this at compile time, so the shape of every PRD it produces is
   * fixed too. Editing it here is what lets a project impose its own
   * conventions, or tell the agent to invoke a particular skill while it still
   * has the whole PRD in context.
   *
   * It is a plain markdown file under .chief/prompts/, so it diffs, reviews and
   * travels with the repository like anything else.
   */

  let { kind = "new" }: { kind?: "new" | "edit" } = $props();

  let prompt = $state<Prompt | null>(null);
  let body = $state("");
  let dirty = $state(false);
  let saving = $state(false);
  let saved = $state(false);
  let error = $state<string | null>(null);

  async function load(): Promise<void> {
    try {
      const p = await api.author.getPrompt(kind);
      prompt = p;
      body = p.body;
      dirty = false;
    } catch (err) {
      error = String(err);
    }
  }

  $effect(() => {
    void kind;
    void load();
  });

  async function save(): Promise<void> {
    saving = true;
    error = null;
    try {
      await api.author.savePrompt(kind, body);
      await load();
      saved = true;
      setTimeout(() => (saved = false), 1500);
    } catch (err) {
      error = String(err);
    } finally {
      saving = false;
    }
  }

  async function reset(): Promise<void> {
    await api.author.resetPrompt(kind);
    await load();
  }
</script>

<div class="editor">
  <div class="head">
    <span class="badge" class:custom={prompt?.custom}>
      {prompt?.custom ? "customised" : "chief default"}
    </span>
    {#if prompt}<code class="path">{prompt.path}</code>{/if}
    <span class="spacer"></span>
    {#if dirty}<span class="dirty">unsaved</span>{/if}
    {#if saved}<span class="ok">saved</span>{/if}
    <button disabled={!dirty || saving} onclick={save}>
      {saving ? "Saving…" : "Save"}
    </button>
    <button disabled={!prompt?.custom} onclick={reset}>Reset</button>
  </div>

  <textarea
    bind:value={body}
    oninput={() => (dirty = true)}
    spellcheck="false"
    placeholder="The instructions the agent receives…"
  ></textarea>

  <p class="vars">
    <code>{"{{PRD_DIR}}"}</code> becomes the PRD's directory ·
    <code>{"{{CONTEXT}}"}</code> becomes what you type when starting a session.
    Slash commands work here — the agent runs them with the PRD in context.
  </p>

  {#if error}<p class="err">{error}</p>{/if}
</div>

<style>
  .editor {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .badge {
    font-size: 11px;
    padding: 1px 7px;
    border-radius: 3px;
    background: var(--bg-raised);
    color: var(--fg-3);
  }
  .badge.custom {
    color: var(--accent);
  }

  .path,
  .vars code {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  .spacer {
    flex: 1;
  }

  .dirty {
    font-size: 11px;
    color: var(--warn);
  }
  .ok {
    font-size: 11px;
    color: var(--ok);
  }

  textarea {
    min-height: 340px;
    padding: 10px;
    background: var(--bg-raised);
    color: var(--fg-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.55;
    resize: vertical;
    tab-size: 2;
  }

  .vars {
    margin: 0;
    font-size: 12px;
    color: var(--fg-3);
  }

  .err {
    margin: 0;
    font-size: 12px;
    color: var(--danger);
  }

  button {
    padding: 3px 10px;
    background: var(--bg-raised);
    color: var(--fg-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font: inherit;
    cursor: default;
  }
  button:hover:not(:disabled) {
    color: var(--fg-1);
    border-color: var(--fg-3);
  }
  button:disabled {
    opacity: 0.4;
  }
</style>
