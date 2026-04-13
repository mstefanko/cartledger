import { get, post } from './client'
import type {
  MealieStatus,
  MealieRecipe,
  MealieShoppingList,
  ImportedRecipe,
  ImportedShoppingList,
} from '@/types'

export async function getMealieStatus(): Promise<MealieStatus> {
  return get<MealieStatus>('/import/mealie/status')
}

export async function listMealieRecipes(): Promise<MealieRecipe[]> {
  return get<MealieRecipe[]>('/import/mealie/recipes')
}

export async function importMealieRecipe(slug: string): Promise<ImportedRecipe> {
  return post<ImportedRecipe>(`/import/mealie/recipes/${encodeURIComponent(slug)}`)
}

export async function listMealieLists(): Promise<MealieShoppingList[]> {
  return get<MealieShoppingList[]>('/import/mealie/lists')
}

export async function importMealieList(id: string): Promise<ImportedShoppingList> {
  return post<ImportedShoppingList>(`/import/mealie/lists/${encodeURIComponent(id)}`)
}
