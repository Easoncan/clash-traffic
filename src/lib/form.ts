/**
 * Takes values changed elsewhere, such as by the update window, into a form
 * being edited from saved: the form takes those the user has not changed,
 * and keeps the user's unsaved edits of the others.
 */
export function takeChanged<T extends object>(form: T, saved: T, changed: Partial<T>): T {
  const next = { ...form };
  for (const k of Object.keys(changed) as (keyof T)[]) {
    if (form[k] === saved[k]) next[k] = changed[k] as T[keyof T];
  }
  return next;
}
