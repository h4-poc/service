import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Search } from "lucide-react";
import { ClusterTable } from './ClusterTable';
import { ClusterInfo } from '@/types/cluster';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";
import { useClusterStore } from '@/store/cluster';

interface ClusterListProps {
  onResourceQuota: (cluster: ClusterInfo) => void;
}

export function ClusterList({ onResourceQuota }: ClusterListProps) {
  const {
    searchTerm,
    selectedEnvironment,
    selectedProvider,
    setSearchTerm,
    setSelectedEnvironment,
    setSelectedProvider,
    getFilteredClusters
  } = useClusterStore();

  const filteredClusters = getFilteredClusters();

  return (
    <Card>
      <div className="flex items-center justify-between p-4 border-b bg-muted/50">
        <div className="flex items-center space-x-4">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
            <Input
              placeholder="Search clusters by name or environment..."
              className="w-[300px] pl-9 bg-background"
              type="search"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
            />
          </div>
          <div className="flex items-center space-x-2">
            <Select value={selectedEnvironment} onValueChange={setSelectedEnvironment}>
              <SelectTrigger className="w-[180px] bg-background">
                <SelectValue placeholder="All Environments" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="All Environments">All Environments</SelectItem>
                <SelectItem value="SIT">SIT</SelectItem>
                <SelectItem value="UAT">UAT</SelectItem>
                <SelectItem value="PRD">PRD</SelectItem>
              </SelectContent>
            </Select>
            <Select value={selectedProvider} onValueChange={setSelectedProvider}>
              <SelectTrigger className="w-[180px] bg-background">
                <SelectValue placeholder="All Providers" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="All Providers">All Providers</SelectItem>
                <SelectItem value="GKE">GKE</SelectItem>
                <SelectItem value="OCP">OCP</SelectItem>
                <SelectItem value="AKS">AKS</SelectItem>
                <SelectItem value="EKS">EKS</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
      </div>
      <ClusterTable
        clusters={filteredClusters}
        onResourceQuota={onResourceQuota}
      />
    </Card>
  );
}