
import { CommonModule } from '@angular/common';
import { Component, Input } from '@angular/core';
import type { DomainRecord } from '../../types/domain';
import { daysUntil, formatDate } from '../../utils/format';
import { parsePermittedCategories } from '../../utils/waste-category';
import { StatusBadgeComponent } from './status-badge.component';

@Component({
  selector: 'app-license-panel',
  standalone: true,
  imports: [CommonModule, StatusBadgeComponent],
  template: `
    <section class="license-panel" aria-label="许可资质摘要">
      <header><strong>许可资质摘要</strong><span>{{ records.length }} 条记录</span></header>
      <div *ngIf="records.length; else empty" class="evidence-strip">
        <article *ngFor="let item of records.slice(0, 4)">
          <div><strong>{{ item.permitNumber || item.licenseNumber }}</strong><span>{{ item.name }}</span></div>
          <app-status-badge [status]="item.status" />
          <div *ngIf="categories(item).length" class="category-chips">
            <span class="category-chip" *ngFor="let code of categories(item)">{{ code }}</span>
          </div>
          <small *ngIf="!categories(item).length" class="category-empty">暂未登记可转运类别</small>
          <small [class.expiring]="remaining(item) !== null && remaining(item)! < 30">
            有效期至 {{ formatDate(expiry(item)) }}
          </small>
        </article>
      </div>
      <ng-template #empty><div class="empty">暂无许可资质</div></ng-template>
    </section>
  `
})
export class LicensePanelComponent {
  @Input() records: DomainRecord[] = [];
  readonly formatDate = formatDate;
  expiry(item: DomainRecord): string { return item.permitExpiresAt || item.licenseExpiresAt || ''; }
  remaining(item: DomainRecord): number | null { return daysUntil(this.expiry(item)); }
  categories(item: DomainRecord): string[] {
    // 优先使用后端按当前许可实时解析的字段，缺失时回退到本地解析，保证历史接口兼容。
    return item.permittedCategoryCodes?.length ? item.permittedCategoryCodes : parsePermittedCategories(item.wasteCategories);
  }
}
