/** A thinking level as the composer names it; an empty level is the model's default. */
export const thinkingLevelName = (level: string) => level ? level[0].toUpperCase() + level.slice(1) : "Default";
