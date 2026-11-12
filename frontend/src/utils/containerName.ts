/** A label is for presentation only; requests and task matching keep the raw name. */
export function containerDisplayName(container?: { name: string; display_name?: string } | null): string {
  return container?.display_name || container?.name || ''
}
