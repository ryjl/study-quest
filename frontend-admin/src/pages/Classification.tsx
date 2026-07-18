import { useSearchParams } from 'react-router-dom';
import { Tags as TagsIcon, Bookmark } from 'lucide-react';
import { PageHeader } from '../components/PageHeader';
import { Tabs, type TabItem } from '../components/ui';
import { SubjectsTable } from './Subjects';
import { TagsTable } from './Tags';

// Merged "分类与标签" page. Subjects (学段/学科) and tags (自由分类) live as
// two tabs under one page so the admin sees both taxonomies together. The
// active tab is mirrored to the URL query (?tab=subjects|tags) so deep-links
// survive refresh and can be shared; updates use `replace` so we don't push a
// new history entry per click.
const TABS: TabItem[] = [
  { key: 'subjects', label: '科目', icon: <TagsIcon size={14} /> },
  { key: 'tags', label: '标签', icon: <Bookmark size={14} /> },
];

const VALID_TABS = new Set(TABS.map((t) => t.key));

export function Classification() {
  const [searchParams, setSearchParams] = useSearchParams();
  const raw = searchParams.get('tab');
  const activeTab = raw && VALID_TABS.has(raw) ? raw : 'subjects';

  const onTabChange = (key: string) => {
    setSearchParams({ tab: key }, { replace: true });
  };

  return (
    <div>
      <PageHeader
        title="分类与标签"
        description="管理课程分类与标签。科目用于学段/学科划分，标签用于自由分类。"
      />
      <div className="mb-6">
        <Tabs tabs={TABS} value={activeTab} onChange={onTabChange} />
      </div>
      <div className="mt-6">
        {activeTab === 'subjects' ? <SubjectsTable /> : <TagsTable />}
      </div>
    </div>
  );
}
