// shareLinkAddress is the absolute address a Share Link is opened at.
export function shareLinkAddress(url: string) {
  return new URL(url, window.location.origin).toString();
}
