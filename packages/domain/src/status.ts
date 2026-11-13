/** Status vocabulary shared by both apps; colours map to design tokens of the same name. */
export const statusTone = {
  todo: "amber",
  waiting: "blue",
  done: "green",
  allotted: "green",
  missed: "red",
  overdue: "red",
  notAllotted: "grey",
} as const;

export type StatusKey = keyof typeof statusTone;
export type Tone = (typeof statusTone)[StatusKey];
