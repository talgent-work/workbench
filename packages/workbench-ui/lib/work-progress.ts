import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
export interface WorkProgress {
  updatedAt: Date;
  items: Array<{ content: string; status: string; activeForm?: string }>;
}

interface ProtoWorkProgressLike {
  items?: Array<{
    content?: string;
    status?: string;
    activeForm?: string;
  }>;
  updatedAt?: Timestamp;
}

export function adaptWorkProgress(progress: ProtoWorkProgressLike | undefined): WorkProgress | null {
  if (!progress?.items || progress.items.length === 0) {
    return null;
  }

  const items = progress.items.flatMap((item): WorkProgress["items"] => {
    if (!item.content) {
      return [];
    }
    return [{
      content: item.content,
      status: item.status || "pending",
      ...(item.activeForm ? { activeForm: item.activeForm } : {}),
    }];
  });

  if (items.length === 0) {
    return null;
  }

  return {
    updatedAt: progress.updatedAt ? timestampDate(progress.updatedAt) : new Date(0),
    items,
  };
}
