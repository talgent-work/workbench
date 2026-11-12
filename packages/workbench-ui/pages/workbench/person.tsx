import { Avatar, Label } from "@heroui/react";
import type { DirectoryUser } from "@talgent/contracts/agent/public/v2/work_pb";
import { avatarInitial, personName, safeAvatarUrl } from "../../lib/user-display";

/** A person of the User Directory: avatar, name and username. */
export function Person({ user }: { user: DirectoryUser }) {
  const name = personName(user);
  const avatar = safeAvatarUrl(user.avatarUrl);
  return <span className="flex min-w-0 items-center gap-2">
    <Avatar size="sm" className="size-6 shrink-0">{avatar && <Avatar.Image src={avatar} alt="" />}<Avatar.Fallback className="text-[10px]">{avatarInitial(name, "", "?")}</Avatar.Fallback></Avatar>
    <Label className="truncate">{name}</Label>{user.username && <span className="truncate text-xs text-muted">@{user.username}</span>}
  </span>;
}
