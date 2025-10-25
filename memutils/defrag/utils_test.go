package defrag

func DefragContextWithMoves[T any](moves []DefragmentationMove[T]) MetadataDefragContext[T] {
	return MetadataDefragContext[T]{moves: moves}
}
