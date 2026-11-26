// The daily chart and the heatmap highlight the same day in each other:
// bars holds the day (days ago) the heatmap looks at, heat the one the
// daily chart does.
export interface ChartLink {
  bars: number | null;
  heat: number | null;
}
