import styles from "./App.module.css";

export function App() {
  return (
    <main className={styles.shell}>
      <section aria-labelledby="moment-title" className={styles.content}>
        <p className={styles.eyebrow}>Portable personal timeline</p>
        <h1 id="moment-title">Moment</h1>
        <p>Capture now. Find later.</p>
      </section>
    </main>
  );
}
