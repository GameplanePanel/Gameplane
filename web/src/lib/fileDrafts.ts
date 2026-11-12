interface Draft {
  text: string;
  saved: string;
  error?: string;
  deleting?: boolean;
  pending?: Promise<boolean>;
  timer?: ReturnType<typeof setTimeout>;
}

// Each workspace belongs to one authorized server incarnation and query client.
// Draft contents stay in this browser session and never enter persistent storage.
export class FileDrafts {
  private readonly files = new Map<string, Draft>();
  private readonly listeners = new Set<() => void>();
  private readonly removing = new Set<string>();
  private revision = 0;
  private enabled = true;

  constructor(private readonly write: (path: string, text: string) => Promise<void>) {}

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };
  snapshot = () => this.revision;
  private changed() {
    this.revision++;
    this.listeners.forEach((listener) => listener());
  }

  paths() { return Array.from(this.files.keys()); }
  get(path: string) { return this.files.get(path); }
  open(path: string, text: string) {
    if (!this.files.has(path) && ![...this.removing].some((root) => path === root || path.startsWith(root + "/"))) {
      this.files.set(path, { text, saved: text });
      this.changed();
    }
  }

  setEnabled(enabled: boolean) {
    this.enabled = enabled;
    for (const [path, draft] of this.files) {
      clearTimeout(draft.timer);
      if (enabled && !draft.error && draft.text !== draft.saved) this.schedule(path, draft);
    }
  }

  private schedule(path: string, draft: Draft) {
    clearTimeout(draft.timer);
    draft.timer = setTimeout(() => { void this.save(path); }, 1200);
  }

  edit(path: string, text: string) {
    const draft = this.get(path);
    if (!draft || draft.deleting || !this.enabled || draft.text === text) return;
    draft.text = text;
    draft.error = undefined;
    this.schedule(path, draft);
    this.changed();
  }

  save(path: string): Promise<boolean> {
    const draft = this.get(path);
    if (!draft || draft.deleting || !this.enabled) return Promise.resolve(false);
    clearTimeout(draft.timer);
    if (!draft.pending) {
      draft.pending = this.flush(path, draft).finally(() => {
        draft.pending = undefined;
        this.changed();
      });
      this.changed();
    }
    return draft.pending;
  }

  private async flush(path: string, draft: Draft) {
    draft.error = undefined;
    try {
      while (this.enabled && !draft.deleting && draft.saved !== draft.text) {
        const body = draft.text;
        await this.write(path, body);
        draft.saved = body;
      }
      return draft.text === draft.saved;
    } catch (err) {
      clearTimeout(draft.timer);
      draft.error = (err as Error).message;
      return false;
    }
  }

  async close(path: string) {
    const draft = this.get(path);
    if (!draft || draft.deleting) return false;
    if ((draft.pending || draft.text !== draft.saved) && !await this.save(path)) return false;
    return draft.text === draft.saved && this.discard(path);
  }

  discard(path: string) {
    const draft = this.get(path);
    if (!draft || draft.pending || draft.deleting) return false;
    clearTimeout(draft.timer);
    this.files.delete(path);
    this.changed();
    return true;
  }

  async remove(path: string, operation: () => Promise<void>) {
    this.removing.add(path);
    const affected = [...this.files].filter(([key]) => key === path || key.startsWith(path + "/"));
    for (const [, draft] of affected) {
      draft.deleting = true;
      clearTimeout(draft.timer);
    }
    this.changed();
    try {
      await Promise.all(affected.map(([, draft]) => draft.pending));
      await operation();
      for (const [key] of affected) this.files.delete(key);
    } finally {
      this.removing.delete(path);
      for (const [, draft] of affected) draft.deleting = false;
      this.changed();
    }
  }
}
