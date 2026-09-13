import type { Photo } from "../../api/types";
import { useInfiniteSentinel } from "../../hooks/useInfiniteSentinel";
import { PhotoTile } from "./PhotoTile";

type Props = {
  photos: Photo[];
  from: string;
  hasMore: boolean;
  onLoadMore: () => void;
};

export function PhotoGrid({ photos, from, hasMore, onLoadMore }: Props) {
  const sentinel = useInfiniteSentinel<HTMLDivElement>(hasMore, onLoadMore);
  return (
    <div className="p-2">
      <div className="grid grid-cols-3 gap-1 sm:grid-cols-4 md:grid-cols-6 lg:grid-cols-8">
        {photos.map((p) => (
          <PhotoTile key={p.id} photo={p} from={from} />
        ))}
      </div>
      {hasMore && <div ref={sentinel} className="h-8" data-testid="grid-sentinel" />}
    </div>
  );
}
