function AIAssistButton() {
  return (
    <div>
      <button
        type="button"
        disabled
        title="Coming soon"
        aria-label="AI assist — coming soon"
        className="inline-flex items-center gap-2 px-3 py-2 rounded-xl border border-neutral-200 bg-neutral-50 text-caption text-neutral-400 cursor-not-allowed"
      >
        <span aria-hidden>✨</span>
        AI assist — coming soon
      </button>
    </div>
  )
}

export default AIAssistButton
