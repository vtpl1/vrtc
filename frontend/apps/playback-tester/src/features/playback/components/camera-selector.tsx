import { useListCamerasQuery } from '../api';
import type { CameraInfo } from '../api';
import styles from './camera-selector.module.css';

interface CameraSelectorProps {
  selectedId: string | null;
  onSelect(camera: CameraInfo): void;
}

export function CameraSelector({ selectedId, onSelect }: CameraSelectorProps) {
  const { data, isLoading, error } = useListCamerasQuery();

  if (isLoading) return <span className="text-sm text-muted">Loading cameras...</span>;
  if (error) return <span className="text-sm badge badge--danger">Failed to load cameras</span>;

  const cameras = data?.items ?? [];
  if (cameras.length === 0) return <span className="text-sm text-muted">No cameras found</span>;

  return (
    <select
      className="select"
      value={selectedId ?? ''}
      onChange={(e) => {
        const cam = cameras.find((c) => c.cameraId === e.target.value);
        if (cam) onSelect(cam);
      }}
      aria-label="Select camera"
    >
      <option value="" disabled>
        Select a camera
      </option>
      {cameras.map((cam) => (
        <option key={cam.cameraId} value={cam.cameraId}>
          {cam.name} ({cam.cameraId}) — {cam.state}
        </option>
      ))}
    </select>
  );
}
