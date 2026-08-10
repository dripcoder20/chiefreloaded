<script lang="ts">
  import { answerQuestion } from "../stores/app.svelte";
  import type { Question } from "../platform";

  /**
   * One blocking decision, rendered as one dialog.
   *
   * The engine sends the whole decision at once — the options, and the fields
   * that qualify them — because two modal prompts before a run starts is one too
   * many. So everything here belongs to a single answer: choosing an option
   * submits the fields alongside it.
   *
   * A field with choices is a selection, a field without is free text, and a
   * read-only field is a decision already made — shown rather than offered,
   * which is why it is not simply a disabled control.
   */
  let { question }: { question: Question } = $props();

  let values = $state<Record<string, string>>({});
  const fields = $derived(question.inputs ?? []);

  // Non-reactive on purpose: it guards the seeding below, and making it state
  // would re-run that effect as it wrote.
  let seededFor = "";

  // Seeded from what the engine preselected, so an option chosen without
  // touching a field still carries the intended value. Re-seeded only when the
  // question itself changes — a refreshed snapshot must not discard typing.
  $effect(() => {
    if (seededFor === question.id) return;
    seededFor = question.id;
    const seeded: Record<string, string> = {};
    for (const field of question.inputs ?? []) seeded[field.key] = field.value ?? "";
    values = seeded;
  });

  function choose(optionId: string): void {
    void answerQuestion(question.id, optionId, { ...values });
  }
</script>

<!-- A question blocks its run, so it belongs above everything else. -->
<div class="question" role="alertdialog" aria-label={question.title}>
  <div class="what">
    <strong>{question.title}</strong>
    {#if question.body}<p>{question.body}</p>{/if}
  </div>

  {#if fields.length > 0}
    <div class="fields">
      {#each fields as field (field.key)}
        {#if field.readOnly}
          <div class="field">
            <span class="field-label">{field.label}</span>
            <span class="settled">{field.hint || field.value}</span>
          </div>
        {:else if field.choices && field.choices.length > 0}
          <fieldset class="field">
            <legend class="field-label">{field.label}</legend>
            {#each field.choices as choice (choice.value)}
              <label class="choice">
                <input
                  type="radio"
                  name={`${question.id}-${field.key}`}
                  value={choice.value}
                  checked={values[field.key] === choice.value}
                  onchange={() => (values[field.key] = choice.value)}
                />
                <span>{choice.label}</span>
                {#if choice.hint}<span class="hint">{choice.hint}</span>{/if}
              </label>
            {/each}
          </fieldset>
        {:else}
          <label class="field">
            <span class="field-label">{field.label}</span>
            <input
              class="mono"
              value={values[field.key] ?? ""}
              placeholder={field.placeholder}
              oninput={(e) => (values[field.key] = e.currentTarget.value)}
            />
          </label>
        {/if}
      {/each}
    </div>
  {/if}

  <div class="options">
    {#each question.options as option (option.id)}
      <button
        class:recommended={option.recommended}
        class:destructive={option.destructive}
        onclick={() => choose(option.id)}
        title={option.hint}
      >
        {option.label}
      </button>
    {/each}
  </div>
</div>

<style>
  .question {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 10px 14px;
    background: var(--bg-raised);
    border-bottom: 1px solid var(--warn);
  }
  .question p {
    margin: 2px 0 0;
    color: var(--fg-2);
  }
  .what {
    max-width: 42ch;
  }

  .fields {
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .field {
    display: flex;
    align-items: center;
    gap: 6px;
    border: 0;
    margin: 0;
    padding: 0;
  }
  .field-label {
    color: var(--fg-2);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0;
  }
  .choice {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .hint,
  .settled {
    color: var(--fg-2);
  }

  .options {
    display: flex;
    gap: 6px;
    margin-left: auto;
  }
  .options .recommended {
    border-color: var(--accent);
    color: var(--fg-1);
  }
  .options .destructive {
    border-color: var(--danger);
    color: var(--danger);
  }
</style>
