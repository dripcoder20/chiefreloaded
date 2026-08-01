<script lang="ts">
  // Placeholder shell. The real three-column layout (rail / centre / inspector)
  // lands with M3. This exists so M0 has something that builds, and so the
  // window, asset pipeline and token layer are proven end to end.
</script>

<main>
  <div class="mark">loop</div>
  <p>Engine vendored. UI starts at M3.</p>
</main>

<style>
  main {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    height: 100vh;
    color: var(--fg-3);
  }

  .mark {
    font: 500 24px/1 var(--font-mono);
    color: var(--fg-1);
    letter-spacing: -0.02em;
  }

  p {
    margin: 0;
  }
</style>
