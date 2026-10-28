import {
  accountVerification,
  verificationArtwork,
  type AccountIdentityKey,
} from "./accountVerification";
import styles from "./AccountName.module.css";

export function AccountName({
  identity,
  name = identity.displayName,
  prominent = false,
}: {
  identity: AccountIdentityKey & { displayName: string };
  name?: string;
  prominent?: boolean;
}) {
  const verification = accountVerification(identity);
  const size = prominent
    ? verificationArtwork.prominentSize
    : verificationArtwork.size;
  return (
    <span className={styles.name} style={{ gap: verificationArtwork.nameGap }}>
      <span className={styles.text}>{name}</span>
      {verification ? (
        <svg
          aria-label="认证账号"
          className={styles.badge}
          data-verification={verification}
          fill={verificationArtwork.color}
          height={size}
          role="img"
          style={{ width: size, height: size }}
          viewBox={verificationArtwork.viewBox}
          width={size}
        >
          <path d={verificationArtwork.path} />
        </svg>
      ) : null}
    </span>
  );
}
