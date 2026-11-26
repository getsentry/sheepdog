import { createContext, useContext, useSyncExternalStore, type ReactNode } from 'react'
import type { State, Store } from './store'

const StoreContext = createContext<Store | null>(null)

export function StoreProvider({ store, children }: { store: Store; children: ReactNode }) {
  return <StoreContext.Provider value={store}>{children}</StoreContext.Provider>
}

export function useStore(): Store {
  const store = useContext(StoreContext)
  if (!store) throw new Error('useStore needs a StoreProvider')
  return store
}

export function useAppState(): State {
  const store = useStore()
  return useSyncExternalStore(store.subscribe, store.getState)
}
