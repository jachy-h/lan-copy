<script setup lang="ts">
export interface FileEntry { name: string; path: string; directory: boolean; modified: string; size: number; previewType?: string }
const props = defineProps<{
  item: FileEntry; selected: boolean; tabindex: number; searchResult: boolean; recent: boolean;
  icon: string; type: string; date: string; sizeLabel: string; previewUrl: string; thumbnailUrl: string;
  parts: { text: string; mark: boolean }[];
}>()
const emit = defineEmits<{
  select: [item: FileEntry, event: MouseEvent | KeyboardEvent]; open: [item: FileEntry, trigger: HTMLElement]; navigate: [path: string];
  menu: [event: MouseEvent, item: FileEntry]; key: [event: KeyboardEvent, item: FileEntry];
  dragstart: [event: DragEvent, item: FileEntry]; dragover: [event: DragEvent, item: FileEntry]; dragleave: [event: DragEvent]; drop: [event: DragEvent, item: FileEntry]
}>()
function openName(event: MouseEvent) { event.preventDefault(); emit('open', props.item, event.currentTarget as HTMLElement) }
</script>

<template>
  <article class="file-row explorer-row" :class="{ selected, 'search-result': searchResult }" :data-path="item.path" role="row" :aria-selected="selected" :tabindex="tabindex" draggable="true" @click="emit('select',item,$event)" @dblclick="!($event.target as Element).closest('button,a')&&emit('open',item,$event.currentTarget as HTMLElement)" @keydown="emit('key',$event,item)" @dragstart="emit('dragstart',$event,item)" @dragover="item.directory&&emit('dragover',$event,item)" @dragleave="emit('dragleave',$event)" @drop="item.directory&&emit('drop',$event,item)">
    <div class="file-main name-cell" role="gridcell"><div class="file-icon" :class="{'folder-icon':item.directory}" aria-hidden="true">{{ icon }}<img v-if="!item.directory&&item.previewType?.startsWith('image/')" :src="thumbnailUrl" alt="" loading="lazy" decoding="async" width="128" height="128" draggable="false" @load="($event.currentTarget as HTMLImageElement).parentElement?.classList.add('has-thumbnail')" @error="($event.currentTarget as HTMLImageElement).remove()"></div>
      <button v-if="item.directory" class="file-name folder-name" type="button" :title="item.name" @click.stop="emit('navigate',item.path)"><template v-for="(part,index) in parts" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></button>
      <a v-else-if="item.previewType" class="file-name download-name" :href="previewUrl" :title="item.name" @click="openName"><template v-for="(part,index) in parts" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></a>
      <a v-else class="file-name download-name preview-unavailable" href="#" :title="`${item.name}（该文件格式暂不支持预览）`" @click.prevent="emit('open',item,$event.currentTarget as HTMLElement)"><template v-for="(part,index) in parts" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></a>
      <span v-if="!item.directory&&recent" class="file-tag recent-tag" title="最近 4 小时内修改" aria-label="新：最近 4 小时内修改">新</span>
    </div>
    <div class="file-cell modified-cell" role="gridcell">{{ date }}</div><div class="file-cell type-cell" role="gridcell">{{ type }}</div><div class="file-cell size-cell" role="gridcell">{{ sizeLabel }}</div><div class="explorer-actions"><button class="secondary row-menu-button" type="button" :aria-label="`更多操作：${item.name}`" aria-haspopup="menu" @click="emit('menu',$event,item)">⋯</button></div>
  </article>
</template>
