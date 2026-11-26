import { reduced } from "./state.svelte";

// A reading that follows its value as if pulled by a spring, blurred while
// it is still far off.
export class Spring {
  x = $state(0);
  target = $state(0);
  #v = 0;
  #raf = 0;
  #last = 0;

  constructor(start = 0) {
    this.x = this.target = start;
  }

  get blur(): string {
    const far = Math.abs(this.target - this.x) / Math.max(this.target, 1);
    const b = Math.min(1.5, Math.max(0, far - 0.3) * 2.2);
    return b > 0.05 ? `blur(${b.toFixed(2)}px)` : "";
  }

  set(v: number) {
    this.target = v;
    if (reduced) {
      this.x = v;
      return;
    }
    if (this.#raf || this.x === v) return;
    this.#last = performance.now();
    const step = (now: number) => {
      let dt = Math.min(0.064, (now - this.#last) / 1000);
      this.#last = now;
      let x = this.x;
      while (dt > 0) {
        const h = Math.min(dt, 0.008);
        this.#v += (170 * (this.target - x) - 2 * Math.sqrt(170) * this.#v) * h;
        x += this.#v * h;
        dt -= h;
      }
      if (Math.abs(this.target - x) < 0.01 && Math.abs(this.#v) < 0.01) {
        x = this.target;
        this.#v = 0;
        this.#raf = 0;
      } else this.#raf = requestAnimationFrame(step);
      this.x = x;
    };
    this.#raf = requestAnimationFrame(step);
  }

  reset() {
    cancelAnimationFrame(this.#raf);
    this.#raf = 0;
    this.x = this.target = 0;
    this.#v = 0;
  }
}
