// 学科配色 / 难度色 / 掌握度三档 —— 从小程序 miniprogram/utils/theme.js 移植而来。

export type Mastery = "unmastered" | "reviewing" | "mastered";

// 学科配色：已知学科用设计稿固定色，未知学科按名字哈希落到调色板
const SUBJECT_COLORS: Record<string, string> = {
  数学: "#E63946",
  物理: "#118AB2",
  化学: "#06D6A0",
  英语: "#FFD166",
  语文: "#E63946",
  生物: "#06D6A0",
  政治: "#118AB2",
  历史: "#FFD166",
  地理: "#06D6A0",
};

export const PALETTE = ["#E63946", "#118AB2", "#06D6A0", "#FFD166"];

export function subjectColor(name?: string): string {
  if (name && SUBJECT_COLORS[name]) return SUBJECT_COLORS[name];
  let h = 0;
  for (const ch of String(name || "")) h = (h + ch.charCodeAt(0)) % PALETTE.length;
  return PALETTE[h];
}

export const DIFFICULTY_COLORS: Record<string, string> = {
  难: "#E63946",
  中: "#FFD166",
  易: "#06D6A0",
};

// 掌握度三档：内部值 <-> 中文标签 / 颜色
export const MASTERY: Record<Mastery, { label: string; color: string }> = {
  unmastered: { label: "未掌握", color: "#E63946" },
  reviewing: { label: "复习中", color: "#FFD166" },
  mastered: { label: "已掌握", color: "#06D6A0" },
};

export const MASTERY_KEYS: Mastery[] = ["unmastered", "reviewing", "mastered"];

export const SUBJECTS = [
  "语文",
  "数学",
  "英语",
  "物理",
  "化学",
  "生物",
  "政治",
  "历史",
  "地理",
  "其他",
];

export const DIFFICULTIES = ["易", "中", "难"];
