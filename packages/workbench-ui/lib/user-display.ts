export type UserDisplaySource = {
  userId?: string;
  displayName?: string;
  primaryEmail?: string;
  avatarUrl?: string;
};

export function isUnsafeIdentityText(value: string | undefined, userId?: string): boolean {
  const text = value?.trim() ?? "";
  const id = userId?.trim() ?? "";
  if (!text) return false;
  if (id && text === id) return true;
  if (/^oidc:/i.test(text) || text.includes("#")) return true;
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(text)) return true;
  if (/^sub[-_:]/i.test(text)) return true;
  return false;
}

export function safeDisplayName(value: string | undefined, userId?: string, fallback = "User"): string {
  const text = value?.trim() ?? "";
  if (!text || isUnsafeIdentityText(text, userId)) {
    return fallback;
  }
  return text;
}

export function displayNameForPerson(person: UserDisplaySource | undefined, fallback = "User"): string {
  return safeDisplayName(person?.displayName, person?.userId, fallback);
}

export function safeEmail(value: string | undefined): string {
  const email = value?.trim() ?? "";
  return email.includes("@") && !isUnsafeIdentityText(email) ? email : "";
}

export function safeAvatarUrl(value: string | undefined): string {
  const url = value?.trim() ?? "";
  if (!url || isUnsafeIdentityText(url)) return "";
  return /^https?:\/\//i.test(url) || url.startsWith("/") ? url : "";
}

export function avatarInitial(name: string, email = "", fallback = "U"): string {
  return (name.trim() || email.trim() || fallback).charAt(0).toUpperCase();
}

/** A User Directory person's name as shown: their display name, else username. */
export function personName(user: { userId: string; username: string; displayName: string }) {
  return safeDisplayName(user.displayName, user.userId, user.username || "Someone");
}
